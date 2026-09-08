package main

import (
        "encoding/json"
        "fmt"
        "io"
        "net"
        "net/http"
        "os"
        "os/exec"
        "sort"
        "strings"
        "time"
)

const ModDir = "/data/adb/modules/boot_creator"

var (
        pairedIP         = ""
        authInProgress   = false
        deviceModel      = "Unknown Device"
        deviceResolution = "Unknown"
        authChan         = make(chan bool, 1)
)

func init() {
        out, err := exec.Command("/system/bin/getprop", "ro.product.model").Output()
        if err == nil {
                deviceModel = strings.TrimSpace(string(out))
        }

        resOut, resErr := exec.Command("/system/bin/sh", "-c", "wm size | grep -oE '[0-9]+x[0-9]+' | tail -n 1").Output()
        if resErr == nil {
                res := strings.TrimSpace(string(resOut))
                if res != "" {
                        deviceResolution = res
                }
        }
}

func writeLog(message string) {
        logPath := ModDir + "/boot_creator.log"
        f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
        if err != nil {
                return
        }
        defer f.Close()
        
        timestamp := time.Now().Format("2006-01-02 15:04:05")
        logEntry := fmt.Sprintf("[%s] %s\n", timestamp, message)
        f.WriteString(logEntry)
}

func showToast(msg string) {
        go func() {
                cmd := fmt.Sprintf(`am broadcast -a com.bootcreator.SHOW_TOAST -n com.bootcreator.companion/.ToastReceiver -e msg "%s"`, msg)
                exec.Command("/system/bin/sh", "-c", cmd).Run()
        }()
}

func enableCORS(w *http.ResponseWriter) {
        (*w).Header().Set("Access-Control-Allow-Origin", "*")
        (*w).Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
        (*w).Header().Set("Access-Control-Allow-Headers", "Content-Type")
        (*w).Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
        (*w).Header().Set("Pragma", "no-cache")
        (*w).Header().Set("Expires", "0")
}

func getIP(r *http.Request) string {
        host, _, err := net.SplitHostPort(r.RemoteAddr)
        if err != nil {
                return r.RemoteAddr
        }
        return host
}

func checkHasCustomAnim() bool {
        cacheFile := ModDir + "/saved_paths.txt"
        data, err := os.ReadFile(cacheFile)
        if err != nil {
                return false
        }

        paths := strings.Split(strings.TrimSpace(string(data)), "\n")
        for _, p := range paths {
                p = strings.TrimSpace(p)
                if p == "" {
                        continue
                }

                if info, err := os.Stat(ModDir + p); err == nil && !info.IsDir() {
                        return true
                }
        }

        return false
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        if r.Method == "OPTIONS" { return }

        clientIP := getIP(r)
        if pairedIP != clientIP {
                fmt.Fprintf(w, `{"status": "auth_required"}`)
                return
        }

        hasCustom := checkHasCustomAnim()
        fmt.Fprintf(w, `{"status": "ok", "model": "%s", "resolution": "%s", "has_custom": %t}`, deviceModel, deviceResolution, hasCustom)
}

func requestAuthHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        if r.Method == "OPTIONS" { return }

        if authInProgress {
                fmt.Fprintf(w, `{"status": "busy"}`)
                return
        }

        authInProgress = true
        defer func() { authInProgress = false }()

        exec.Command("su", "2000", "-c", "am start -n com.bootcreator.companion/.PromptActivity").Run()

        select {
        case allowed := <-authChan:
                if allowed {
                        pairedIP = getIP(r)
                        hasCustom := checkHasCustomAnim()
                        fmt.Fprintf(w, `{"status": "ok", "model": "%s", "resolution": "%s", "has_custom": %t}`, deviceModel, deviceResolution, hasCustom)
                        showToast("✨ Boot Creator: Website connected successfully!")
                        writeLog("Connection allowed by user from IP: " + pairedIP)
                } else {
                        fmt.Fprintf(w, `{"status": "denied"}`)
                        showToast("❌ Boot Creator: Connection denied by user.")
                        writeLog("Connection denied by user from IP: " + getIP(r))
                }
        case <-time.After(30 * time.Second):
                fmt.Fprintf(w, `{"status": "timeout"}`)
                writeLog("Connection prompt timed out for IP: " + getIP(r))
        }
}

func authCallbackHandler(w http.ResponseWriter, r *http.Request) {
        allow := r.URL.Query().Get("allow") == "true"
        select { case <-authChan: default: }
        authChan <- allow
        fmt.Fprintf(w, "Received!")
}

func disconnectHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        if r.Method == "OPTIONS" { return }

        clientIP := getIP(r)
        if pairedIP == clientIP {
                pairedIP = ""
                showToast("🔌 Boot Creator: Website disconnected.")
                writeLog("Website disconnected manually by user.")
        }

        fmt.Fprintf(w, `{"status": "disconnected"}`)
}

func cleanHistory(dir string) {
        entries, err := os.ReadDir(dir)
        if err != nil {
                return
        }

        var zips []string
        for _, e := range entries {
                if strings.HasSuffix(e.Name(), ".zip") {
                        zips = append(zips, e.Name())
                }
        }

        if len(zips) > 5 {
                sort.Strings(zips)
                for i := 0; i < len(zips)-5; i++ {
                        base := strings.TrimSuffix(zips[i], ".zip")
                        os.Remove(dir + "/" + base + ".zip")
                        os.Remove(dir + "/" + base + ".gif")
                }
                showToast("🧹 Boot Creator: Automatic cleanup! Old animations were removed from history.")
                writeLog("Automatic history cleanup performed. Oldest animations deleted.")
        }
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        if r.Method == "OPTIONS" { return }

        clientIP := getIP(r)
        if pairedIP != clientIP || pairedIP == "" {
                http.Error(w, "Access Denied", http.StatusUnauthorized)
                return
        }

        r.ParseMultipartForm(50 << 20)
        file, _, err := r.FormFile("bootanimation")
        if err != nil {
                http.Error(w, "Failed to receive file", http.StatusBadRequest)
                writeLog("Error: Failed to receive bootanimation.zip during upload.")
                return
        }
        defer file.Close()

        tempPath := "/data/local/tmp/new_boot.zip"
        tempFile, _ := os.Create(tempPath)
        io.Copy(tempFile, file)
        tempFile.Close()

        historyDir := ModDir + "/history"
        os.MkdirAll(historyDir, 0755)

        timestamp := fmt.Sprintf("%d", time.Now().Unix())
        historyZipPath := historyDir + "/" + timestamp + ".zip"

        exec.Command("/system/bin/sh", "-c", "cp "+tempPath+" "+historyZipPath).Run()

        previewFile, _, errPref := r.FormFile("preview")
        if errPref == nil {
                defer previewFile.Close()
                prefOut, _ := os.Create(historyDir + "/" + timestamp + ".gif")
                io.Copy(prefOut, previewFile)
                prefOut.Close()
        }

        cleanHistory(historyDir)

        cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", tempPath)
        if cmd.Run() != nil {
                fmt.Fprintf(w, `{"status": "error"}`)
                showToast("❌ Boot Creator: Oops... An error occurred while injecting the ZIP!")
                writeLog("Error: Failed to inject new boot animation via inject.sh.")
        } else {
                fmt.Fprintf(w, `{"status": "success"}`)
                showToast("🚀 Boot Creator: New animation injected! Ready for the next boot!")
                writeLog("Success: New boot animation injected and saved to history (ID: " + timestamp + ").")
        }
}

func removeHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        w.Header().Set("Content-Type", "application/json")
        if r.Method == "OPTIONS" { return }

        clientIP := getIP(r)
        if pairedIP != clientIP || pairedIP == "" {
                http.Error(w, `{"status": "error", "message": "Access Denied"}`, http.StatusUnauthorized)
                return
        }

        cmd := exec.Command("/system/bin/sh", ModDir+"/clean.sh")
        if err := cmd.Run(); err != nil {
                fmt.Fprintf(w, `{"status": "error", "message": "Error running clean.sh!"}`)
                writeLog("Error: Failed to run clean.sh during removal.")
                return
        }

        fmt.Fprintf(w, `{"status": "success", "message": "Animation successfully removed!"}`)
        showToast("🗑️ Boot Creator: Custom animation removed. Original boot restored!")
        writeLog("Success: Custom boot animation removed. Restored to stock.")
}

func pullHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        if r.Method == "OPTIONS" { return }

        clientIP := getIP(r)
        if pairedIP != clientIP || pairedIP == "" {
                http.Error(w, "Access Denied", http.StatusUnauthorized)
                return
        }

        source := r.URL.Query().Get("source")
        cacheFile := ModDir + "/saved_paths.txt"
        data, err := os.ReadFile(cacheFile)

        if err != nil {
                http.Error(w, "No paths found", http.StatusNotFound)
                writeLog("Error: Attempted to pull animation, but no saved paths were found.")
                return
        }

        paths := strings.Split(strings.TrimSpace(string(data)), "\n")
        if len(paths) == 0 || paths[0] == "" {
                http.Error(w, "No paths saved", http.StatusNotFound)
                return
        }

        targetPath := paths[0]
        fileToServe := targetPath

        if source == "module" {
                fileToServe = ModDir + targetPath
                showToast("📥 Boot Creator: The website extracted your custom animation from the module!")
                writeLog("Animation pulled by website (Source: Module).")
        } else if source == "system" {
                backupPath := ModDir + "/backup" + targetPath
                if _, err := os.Stat(backupPath); err == nil {
                        fileToServe = backupPath
                } else {
                        fileToServe = targetPath
                }
                showToast("📥 Boot Creator: The website extracted your stock animation!")
                writeLog("Animation pulled by website (Source: Stock System).")
        }

        if _, err := os.Stat(fileToServe); os.IsNotExist(err) {
                http.Error(w, "File not found physically", http.StatusNotFound)
                writeLog("Error: Pulled animation file not found physically at " + fileToServe)
                return
        }

        w.Header().Set("Content-Disposition", "attachment; filename=pulled_bootanimation.zip")
        w.Header().Set("Content-Type", "application/zip")
        http.ServeFile(w, r, fileToServe)
}

func resetHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        w.Header().Set("Content-Type", "application/json")
        if r.Method == "OPTIONS" { return }

        clientIP := getIP(r)
        if pairedIP != clientIP || pairedIP == "" {
                http.Error(w, `{"status": "error", "message": "Access Denied"}`, http.StatusUnauthorized)
                return
        }

        cmd := exec.Command("/system/bin/sh", "-c", "rm -f "+ModDir+"/saved_paths.txt "+ModDir+"/system.prop "+ModDir+"/boot_creator.log && rm -rf "+ModDir+"/system "+ModDir+"/product "+ModDir+"/oem "+ModDir+"/vendor "+ModDir+"/system_ext "+ModDir+"/apex "+ModDir+"/custom "+ModDir+"/history")
        if err := cmd.Run(); err != nil {
                fmt.Fprintf(w, `{"status": "error", "message": "Error resetting the module!"}`)
                writeLog("Error: Failed to reset the module directories.")
                return
        }

        fmt.Fprintf(w, `{"status": "success", "message": "Module reset successfully!"}`)
        showToast("⚠️ Boot Creator: Module has been reset (Troubleshoot).")
        writeLog("Success: Module troubleshooting reset executed. History cleared.")
}

func historyListHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        w.Header().Set("Content-Type", "application/json")
        if r.Method == "OPTIONS" { return }

        historyDir := ModDir + "/history"
        entries, _ := os.ReadDir(historyDir)

        var ids []string
        for _, e := range entries {
                if strings.HasSuffix(e.Name(), ".zip") {
                        ids = append(ids, strings.TrimSuffix(e.Name(), ".zip"))
                }
        }

        sort.Sort(sort.Reverse(sort.StringSlice(ids)))
        json.NewEncoder(w).Encode(ids)
}

func historyPreviewHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        if r.Method == "OPTIONS" { return }

        id := r.URL.Query().Get("id")
        path := ModDir + "/history/" + id + ".gif"

        w.Header().Set("Cache-Control", "max-age=86400")
        http.ServeFile(w, r, path)
}

func historyApplyHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        w.Header().Set("Content-Type", "application/json")
        if r.Method == "OPTIONS" { return }

        id := r.URL.Query().Get("id")
        path := ModDir + "/history/" + id + ".zip"
        tempPath := "/data/local/tmp/apply_history.zip"

        exec.Command("/system/bin/sh", "-c", "cp "+path+" "+tempPath).Run()

        cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", tempPath)
        if cmd.Run() != nil {
                fmt.Fprintf(w, `{"status": "error", "message": "Error injecting history!"}`)
                writeLog("Error: Failed to restore animation from history (ID: " + id + ").")
        } else {
                fmt.Fprintf(w, `{"status": "success", "message": "Animation restored from the past! ✨"}`)
                showToast("⏳ Boot Creator: Past animation restored successfully!")
                writeLog("Success: Animation restored from history (ID: " + id + ").")
        }
}

func historyDeleteHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        w.Header().Set("Content-Type", "application/json")
        if r.Method == "OPTIONS" { return }

        id := r.URL.Query().Get("id")
        os.Remove(ModDir + "/history/" + id + ".zip")
        os.Remove(ModDir + "/history/" + id + ".gif")

        fmt.Fprintf(w, `{"status": "success"}`)
        showToast("🧹 Boot Creator: An old animation was deleted from the vault.")
        writeLog("History item deleted manually by user (ID: " + id + ").")
}

func testAnimHandler(w http.ResponseWriter, r *http.Request) {
        enableCORS(&w)
        w.Header().Set("Content-Type", "application/json")
        if r.Method == "OPTIONS" { return }

        clientIP := getIP(r)
        if pairedIP != clientIP || pairedIP == "" {
                http.Error(w, `{"status": "error", "message": "Access Denied"}`, http.StatusUnauthorized)
                return
        }

        cacheFile := ModDir + "/saved_paths.txt"
        data, err := os.ReadFile(cacheFile)
        if err != nil {
                fmt.Fprintf(w, `{"status": "error", "message": "No saved path found!"}`)
                return
        }
        
        paths := strings.Split(strings.TrimSpace(string(data)), "\n")
        if len(paths) == 0 || paths[0] == "" {
                fmt.Fprintf(w, `{"status": "error", "message": "No saved path found!"}`)
                return
        }
        
        targetPath := strings.TrimSpace(paths[0])
        modulePath := ModDir + targetPath 

        exec.Command("/system/bin/sh", "-c", "umount "+targetPath).Run()
        exec.Command("/system/bin/sh", "-c", "mount -o bind "+modulePath+" "+targetPath).Run()
        exec.Command("/system/bin/sh", "-c", "setprop ctl.start bootanim").Run()

        showToast("👀 Boot Creator: Showing Magic Preview... Look at your screen!")
        writeLog("Preview triggered on device using bind mount.")

        go func() {
                time.Sleep(15 * time.Second) 
                exec.Command("/system/bin/sh", "-c", "setprop ctl.stop bootanim").Run()
                time.Sleep(1 * time.Second)
                exec.Command("/system/bin/sh", "-c", "umount "+targetPath).Run()
                showToast("🏁 Boot Creator: Preview finished!")
                writeLog("Preview finished and unmounted.")
        }()

        fmt.Fprintf(w, `{"status": "success", "message": "Look at your phone!"}`)
}

func main() {
        writeLog("=== Boot Creator Server Started ===")

        http.HandleFunc("/ping", pingHandler)
        http.HandleFunc("/request_auth", requestAuthHandler)
        http.HandleFunc("/auth_callback", authCallbackHandler)
        http.HandleFunc("/disconnect", disconnectHandler)
        http.HandleFunc("/upload", uploadHandler)
        http.HandleFunc("/remove", removeHandler)
        http.HandleFunc("/pull", pullHandler)
        http.HandleFunc("/reset", resetHandler)

        http.HandleFunc("/history/list", historyListHandler)
        http.HandleFunc("/history/preview", historyPreviewHandler)
        http.HandleFunc("/history/apply", historyApplyHandler)
        http.HandleFunc("/history/delete", historyDeleteHandler)

        http.HandleFunc("/test_anim", testAnimHandler)

        fmt.Println("✨ Boot Creator server running on port 4040...")
        http.ListenAndServe("0.0.0.0:4040", nil)
}
