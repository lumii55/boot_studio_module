package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const moduleLogMaxBytes int64 = 1 << 20

const moduleLogKeepBytes int64 = 512 << 10

var moduleLogMu sync.Mutex

func trimModuleLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= moduleLogMaxBytes {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	start := info.Size() - moduleLogKeepBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return
	}
	tail, err := io.ReadAll(io.LimitReader(f, moduleLogKeepBytes))
	if err != nil {
		return
	}
	content := append([]byte("[Boot Animation Studio] Earlier log content trimmed to bound disk usage.\n"), tail...)
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return
	}
	_, _ = out.Write(content)
	_ = out.Close()
}

func writeLog(message string) {
	moduleLogMu.Lock()
	defer moduleLogMu.Unlock()
	logPath := ModDir + "/boot_creator.log"
	trimModuleLog(logPath)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, _ = f.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
}

func showToast(msg string) {
	showToastEvent("", msg)
}

func showToastEvent(key, fallback string, args ...string) {
	go func() {
		commandArgs := []string{
			"broadcast",
			"-a", "com.bootcreator.SHOW_TOAST",
			"-n", "com.bootcreator.companion/.ToastReceiver",
			"-e", "msg", fallback,
		}
		if strings.TrimSpace(key) != "" {
			commandArgs = append(commandArgs, "-e", "key", key)
		}
		if len(args) > 0 && args[0] != "" {
			commandArgs = append(commandArgs, "-e", "arg0", args[0])
		}
		if len(args) > 1 && args[1] != "" {
			commandArgs = append(commandArgs, "-e", "arg0_type", args[1])
		}
		_ = exec.Command("/system/bin/am", commandArgs...).Run()
	}()
}
