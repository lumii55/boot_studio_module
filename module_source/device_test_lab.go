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
	"time"
)

type testStageResponse struct {
	Status        string   `json:"status"`
	HasStaged     bool     `json:"has_staged"`
	PreviewActive bool     `json:"preview_active"`
	SizeBytes     int64    `json:"size_bytes,omitempty"`
	Width         int      `json:"width,omitempty"`
	Height        int      `json:"height,omitempty"`
	FPS           int      `json:"fps,omitempty"`
	FrameCount    int      `json:"frame_count,omitempty"`
	Parts         int      `json:"parts,omitempty"`
	FrameFormat   string   `json:"frame_format,omitempty"`
	HasAudio      bool     `json:"has_audio,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

func startPreviewFromFile(sourcePath string, cleanupSource func()) error {
	finishSource := func() {
		if cleanupSource != nil {
			cleanupSource()
		}
	}

	paths, err := readSavedPaths()
	if err != nil {
		finishSource()
		return errors.New("no valid saved path found")
	}
	if info, err := os.Stat(sourcePath); err != nil || info.IsDir() {
		finishSource()
		return errors.New("preview animation not found")
	}

	targetPaths := make([]string, 0, len(paths))
	for _, targetPath := range paths {
		if globalRegularFileExists(targetPath) {
			targetPaths = append(targetPaths, targetPath)
		}
	}
	if len(targetPaths) == 0 {
		finishSource()
		return errors.New("system animation path not found in global mount namespace")
	}

	cancel, ok := acquirePreview(targetPaths)
	if !ok {
		finishSource()
		return errors.New("preview busy")
	}

	mounted := make([]string, 0, len(targetPaths))
	rollback := func() {
		unmountPreviewTargets(mounted)
		releasePreview(cancel)
		finishSource()
	}
	for _, targetPath := range targetPaths {
		beforeCount, countErr := globalMountCount(targetPath)
		if countErr != nil {
			rollback()
			return errors.New("could not inspect global preview mount namespace")
		}
		if err := runGlobalMountCommand("/system/bin/mount", "-o", "bind", sourcePath, targetPath); err != nil {
			rollback()
			return errors.New("could not mount preview animation in global namespace: " + err.Error())
		}
		mounted = append(mounted, targetPath)
		writePreviewMountMarker(mounted)
		afterCount, countErr := globalMountCount(targetPath)
		if countErr != nil || afterCount <= beforeCount || !globalFilesMatch(sourcePath, targetPath) {
			rollback()
			return errors.New("preview global mount verification failed")
		}
	}

	if err := exec.Command("/system/bin/setprop", "ctl.start", "bootanim").Run(); err != nil {
		rollback()
		return errors.New("could not start boot animation preview")
	}
	writeLog("Preview mounted in global namespace on: " + strings.Join(mounted, ", "))
	go func() {
		select {
		case <-time.After(15 * time.Second):
		case <-cancel:
		}
		cleanupPreview(mounted, cancel)
		finishSource()
		showToastEvent("toast_preview_finished", "🏁 Boot Creator: Preview finished!")
		writeLog("Preview finished and global mounts were removed.")
	}()
	return nil
}

func testStageHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if active, _ := currentPreviewState(); active {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy", Message: "Stop the current preview before staging another animation"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBootAnimationBytes+(8<<20))
	multipartReader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Upload must be multipart/form-data"})
		return
	}
	tempFile, err := os.CreateTemp("/data/local/tmp", "boot_creator_stage_*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create temporary file"})
		return
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	hasAnimation := false
	for {
		part, nextErr := multipartReader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Could not read multipart upload"})
			return
		}
		if part.FormName() == "bootanimation" && !hasAnimation {
			if err := saveMultipartFile(part, tempPath, maxBootAnimationBytes); err != nil {
				_ = part.Close()
				writeJSON(w, http.StatusRequestEntityTooLarge, statusResponse{Status: "error", Message: "bootanimation.zip exceeds the transport safety limit"})
				return
			}
			hasAnimation = true
		} else {
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
		return
	}
	meta, err := inspectBootAnimation(tempPath, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	stageDir := filepath.Dir(stagedAnimationPath())
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare test staging"})
		return
	}
	stageTemp := stagedAnimationPath() + ".tmp"
	_ = os.Remove(stageTemp)
	if err := copyFile(tempPath, stageTemp, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not stage boot animation"})
		return
	}
	if err := os.Rename(stageTemp, stagedAnimationPath()); err != nil {
		_ = os.Remove(stageTemp)
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not commit staged boot animation"})
		return
	}
	if err := os.Chmod(stagedAnimationPath(), 0644); err != nil {
		_ = os.Remove(stagedAnimationPath())
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not set staged animation permissions"})
		return
	}
	if err := setSystemFileContext(stagedAnimationPath()); err != nil {
		_ = os.Remove(stagedAnimationPath())
		writeLog("Error: Could not label staged animation for boot preview: " + err.Error())
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare staged animation for boot preview"})
		return
	}
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeLog(fmt.Sprintf("Test animation staged (%d bytes).", meta.SizeBytes))
	writeJSON(w, http.StatusOK, testStageResponse{Status: "success", HasStaged: true, PreviewActive: false, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, FPS: meta.FPS, FrameCount: meta.FrameCount, Parts: meta.Parts, FrameFormat: meta.FrameFormat, HasAudio: meta.HasAudio, Warnings: warnings})
}

func testStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	active, _ := currentPreviewState()
	meta, err := stagedMetadata()
	if err != nil {
		writeJSON(w, http.StatusOK, testStageResponse{Status: "success", HasStaged: false, PreviewActive: active})
		return
	}
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeJSON(w, http.StatusOK, testStageResponse{Status: "success", HasStaged: true, PreviewActive: active, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, FPS: meta.FPS, FrameCount: meta.FrameCount, Parts: meta.Parts, FrameFormat: meta.FrameFormat, HasAudio: meta.HasAudio, Warnings: warnings})
}

func testStartHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	path := stagedAnimationPath()
	if err := validateBootAnimation(path); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "No valid staged animation"})
		return
	}
	previewSource, err := prepareStagedPreviewSource()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	if err := startPreviewFromFile(previewSource, func() { _ = os.Remove(previewSource) }); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "preview busy" {
			status = http.StatusConflict
		}
		writeJSON(w, status, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	showToastEvent("toast_preview_staged", "👀 Boot Creator: Showing staged preview. Look at your screen!")
	writeLog("Staged preview started on device.")
	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Preview started"})
}

func testStopHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if !cancelActivePreview() {
		writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "No preview was running"})
		return
	}
	waitForPreviewStop(3 * time.Second)
	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Preview stopped"})
}

func testClearHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if err := clearStagedTestState(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not clear staged animation"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_test_staging_cleared", "🧪 Boot Creator: Test staging cleared.")
}

func testApplyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if active, _ := currentPreviewState(); active {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy", Message: "Stop the preview before applying the staged animation"})
		return
	}
	stagePath := stagedAnimationPath()
	if err := validateBootAnimation(stagePath); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "No valid staged animation"})
		return
	}
	historyDir := filepath.Join(ModDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare history"})
		return
	}
	id := newHistoryID(historyDir)
	historyZipPath := filepath.Join(historyDir, id+".zip")
	if err := linkOrCopyFile(stagePath, historyZipPath, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save staged animation to history"})
		return
	}
	meta, err := inspectBootAnimation(historyZipPath, id)
	if err != nil {
		_ = os.Remove(historyZipPath)
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not inspect staged animation"})
		return
	}
	_ = writeHistoryMetadata(id, meta)
	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", historyZipPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(historyZipPath)
		_ = os.Remove(historyMetadataPath(id))
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Failed to inject staged boot animation"})
		writeLog("Error: Failed to apply staged animation: " + strings.TrimSpace(string(output)))
		return
	}
	_ = os.Remove(stagePath)
	cleanHistory(historyDir)
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeJSON(w, http.StatusOK, uploadResponse{Status: "success", HistoryID: id, SizeBytes: meta.SizeBytes, Warnings: warnings})
	showToastEvent("toast_test_applied", "🚀 Boot Creator: Tested animation applied and saved to history!")
	writeLog("Success: Staged animation applied and saved to history (ID: " + id + ").")
}

func testAnimHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	paths, err := readSavedPaths()
	if err != nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "No valid saved path found"})
		return
	}
	modulePath := ""
	for _, targetPath := range paths {
		candidate := ModDir + targetPath
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			modulePath = candidate
			break
		}
	}
	if modulePath == "" {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Custom animation not found in module"})
		return
	}
	if err := startPreviewFromFile(modulePath, nil); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "preview busy" {
			status = http.StatusConflict
		}
		writeJSON(w, status, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	showToastEvent("toast_preview_device", "👀 Boot Creator: Showing device preview. Look at your screen!")
	writeLog("Preview triggered on device using bind mount.")
	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Preview started"})
}
