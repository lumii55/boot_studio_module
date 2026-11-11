package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type uploadResponse struct {
	Status    string   `json:"status"`
	HistoryID string   `json:"history_id,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	multipartReader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Upload must be multipart/form-data"})
		return
	}

	tempFile, err := os.CreateTemp("/data/local/tmp", "boot_creator_upload_*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create temporary file"})
		return
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	previewTemp, err := os.CreateTemp("/data/local/tmp", "boot_creator_preview_*.webm")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create temporary preview file"})
		return
	}
	previewTempPath := previewTemp.Name()
	_ = previewTemp.Close()
	_ = os.Remove(previewTempPath)
	defer os.Remove(previewTempPath)

	hasAnimation := false
	hasPreview := false
	for {
		part, nextErr := multipartReader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Could not read multipart upload"})
			return
		}

		switch part.FormName() {
		case "bootanimation":
			if hasAnimation {
				_ = part.Close()
				writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Only one bootanimation file is allowed"})
				return
			}
			if err := saveMultipartFile(part, tempPath, maxBootAnimationBytes); err != nil {
				_ = part.Close()
				writeJSON(w, http.StatusRequestEntityTooLarge, statusResponse{Status: "error", Message: "bootanimation.zip exceeds the transport safety limit"})
				writeLog("Error: bootanimation.zip exceeded the transport safety limit.")
				return
			}
			hasAnimation = true
		case "preview":
			if !hasPreview {
				if err := saveMultipartFile(part, previewTempPath, maxPreviewBytes); err == nil {
					hasPreview = true
				} else {
					_ = os.Remove(previewTempPath)
					writeLog("Warning: Preview file was rejected because it exceeded the allowed size.")
				}
			}
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
		}
		_ = part.Close()
	}

	if !hasAnimation {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Failed to receive bootanimation.zip"})
		return
	}
	if err := validateBootAnimation(tempPath); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		writeLog("Error: Invalid boot animation upload: " + err.Error())
		return
	}
	if hasPreview {
		if err := validatePreviewFile(previewTempPath); err != nil {
			_ = os.Remove(previewTempPath)
			hasPreview = false
			writeLog("Warning: Preview file was rejected because it was not a valid WebM file.")
		}
	}

	historyDir := ModDir + "/history"
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare history"})
		return
	}
	id := newHistoryID(historyDir)
	historyZipPath := filepath.Join(historyDir, id+".zip")
	if err := moveFileOrCopy(tempPath, historyZipPath, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save history item"})
		return
	}

	meta, err := inspectBootAnimation(historyZipPath, id)
	if err != nil {
		_ = os.Remove(historyZipPath)
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not inspect history item"})
		return
	}
	_ = writeHistoryMetadata(id, meta)

	previewPath := ""
	if hasPreview {
		previewPath = filepath.Join(historyDir, id+".webm")
		if err := moveFileOrCopy(previewTempPath, previewPath, 0644); err != nil {
			previewPath = ""
			writeLog("Warning: Could not persist the supplied history preview.")
		}
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", historyZipPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(historyZipPath)
		_ = os.Remove(historyMetadataPath(id))
		if previewPath != "" {
			_ = os.Remove(previewPath)
		}
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Failed to inject boot animation"})
		writeLog("Error: Failed to inject new boot animation via inject.sh: " + strings.TrimSpace(string(output)))
		return
	}

	cleanHistory(historyDir)
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeJSON(w, http.StatusOK, uploadResponse{Status: "success", HistoryID: id, SizeBytes: meta.SizeBytes, Warnings: warnings})
	showToastEvent("toast_animation_applied", "🚀 Boot Creator: New animation injected and ready for the next boot!")
	writeLog("Success: New boot animation injected and saved to history (ID: " + id + ").")
}

func removeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/clean.sh")
	if err := cmd.Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error running clean.sh"})
		writeLog("Error: Failed to run clean.sh during removal.")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Animation successfully removed"})
	showToastEvent("toast_animation_removed", "🗑️ Boot Creator: Custom animation removed. Original boot restored!")
	writeLog("Success: Custom boot animation removed. Restored to stock.")
}

func resolveStockPullPath(targetPath string, backupAvailable, customApplied bool) (string, error) {
	if backupAvailable {
		return ModDir + "/backup" + targetPath, nil
	}
	if customApplied {
		return "", errors.New("stock animation is unavailable while a custom animation is active")
	}
	return targetPath, nil
}

func pullHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	source := r.URL.Query().Get("source")
	if source != "module" && source != "system" {
		http.Error(w, "Invalid source", http.StatusBadRequest)
		return
	}

	paths, err := readSavedPaths()
	if err != nil {
		http.Error(w, "No valid paths found", http.StatusNotFound)
		writeLog("Error: Attempted to pull animation, but no valid saved paths were found.")
		return
	}

	targetPath := paths[0]
	fileToServe := targetPath

	if source == "module" {
		fileToServe = ModDir + targetPath
		showToastEvent("toast_pull_custom", "📥 Boot Creator: The website extracted your custom animation from the module!")
		writeLog("Animation pulled by website (Source: Module).")
	} else {
		backupPath := ModDir + "/backup" + targetPath
		backupInfo, backupErr := os.Stat(backupPath)
		backupAvailable := backupErr == nil && !backupInfo.IsDir()
		fileToServe, err = resolveStockPullPath(targetPath, backupAvailable, checkHasCustomAnim())
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			writeLog("Error: Stock animation pull refused because no clean stock source is available while a custom animation is active.")
			return
		}
	}

	info, err := os.Stat(fileToServe)
	if err != nil || info.IsDir() {
		http.Error(w, "File not found", http.StatusNotFound)
		writeLog("Error: Pulled animation file not found at " + fileToServe)
		return
	}

	if source == "system" {
		showToastEvent("toast_pull_stock", "📥 Boot Creator: The website extracted your stock animation!")
		writeLog("Animation pulled by website (Source: Stock System).")
	}

	w.Header().Set("Content-Disposition", "attachment; filename=pulled_bootanimation.zip")
	w.Header().Set("Content-Type", "application/zip")
	http.ServeFile(w, r, fileToServe)
}

func performRescan(w http.ResponseWriter, r *http.Request, legacy bool) {
	paths, err := runPathRescan()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not rescan module paths"})
		writeLog("Error: Module path rescan failed: " + err.Error())
		return
	}

	message := fmt.Sprintf("Module paths rescanned successfully (%d found)", len(paths))
	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: message, HasCustom: checkHasCustomAnim()})
	showToastEvent("toast_rescan", "🔎 Boot Creator: Boot animation paths rescanned successfully.")
	if legacy {
		writeLog("Success: Legacy /reset request performed a non-destructive path rescan.")
	} else {
		writeLog(fmt.Sprintf("Success: Module paths rescanned. %d valid paths cached.", len(paths)))
	}
}

func rescanHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	performRescan(w, r, false)
}

func resetHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	performRescan(w, r, true)
}

func factoryResetHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if r.URL.Query().Get("confirm") != "erase_module_data" {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Factory reset confirmation required"})
		return
	}

	if paths, err := readSavedPaths(); err == nil {
		for _, targetPath := range paths {
			_ = os.Remove(ModDir + targetPath)
		}
	}

	files := []string{
		ModDir + "/saved_paths.txt",
		ModDir + "/.paths_schema",
		ModDir + "/system.prop",
		ModDir + "/boot_creator.log",
		ModDir + "/boot_creator.previous.log",
		ModDir + "/.preview_mounts",
	}
	dirs := []string{
		ModDir + "/system",
		ModDir + "/product",
		ModDir + "/oem",
		ModDir + "/vendor",
		ModDir + "/odm",
		ModDir + "/system_ext",
		ModDir + "/apex",
		ModDir + "/custom",
		ModDir + "/history",
		ModDir + "/playlist",
		ModDir + "/staging",
		ModDir + "/backup",
	}

	for _, filePath := range files {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error resetting the module"})
			writeLog("Error: Failed to reset module file: " + filePath)
			return
		}
	}

	for _, dirPath := range dirs {
		if err := os.RemoveAll(dirPath); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error resetting the module"})
			writeLog("Error: Failed to reset module directory: " + dirPath)
			return
		}
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Module data reset successfully"})
	showToastEvent("toast_reset", "⚠️ Boot Creator: Module data was reset for troubleshooting.")
	writeLog("Success: Destructive module factory reset executed.")
}
