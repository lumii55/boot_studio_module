package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type historyMetadata struct {
	Version     int    `json:"version"`
	CreatedAt   int64  `json:"created_at"`
	SizeBytes   int64  `json:"size_bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	FrameCount  int    `json:"frame_count"`
	Parts       int    `json:"parts"`
	FrameFormat string `json:"frame_format"`
	HasAudio    bool   `json:"has_audio"`
}

type historyItemResponse struct {
	ID          string `json:"id"`
	CreatedAt   int64  `json:"created_at"`
	SizeBytes   int64  `json:"size_bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	FrameCount  int    `json:"frame_count"`
	Parts       int    `json:"parts"`
	FrameFormat string `json:"frame_format"`
	HasAudio    bool   `json:"has_audio"`
	HasPreview  bool   `json:"has_preview"`
	PreviewType string `json:"preview_type,omitempty"`
}

func cleanHistory(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var zips []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".zip") {
			zips = append(zips, entry.Name())
		}
	}

	if len(zips) <= historyLimit {
		return
	}

	sort.Strings(zips)
	for i := 0; i < len(zips)-historyLimit; i++ {
		base := strings.TrimSuffix(zips[i], ".zip")
		_ = os.Remove(filepath.Join(dir, base+".zip"))
		_ = os.Remove(filepath.Join(dir, base+".webm"))
		_ = os.Remove(filepath.Join(dir, base+".gif"))
		_ = os.Remove(filepath.Join(dir, base+".json"))
	}
	showToastEvent("toast_history_cleanup", "🧹 Boot Creator: Automatic cleanup removed old animations from history.")
	writeLog("Automatic history cleanup performed. Oldest animations deleted.")
}

func newHistoryID(dir string) string {
	id := time.Now().UnixMilli()
	for {
		candidate := strconv.FormatInt(id, 10)
		if _, err := os.Stat(filepath.Join(dir, candidate+".zip")); os.IsNotExist(err) {
			return candidate
		}
		id++
	}
}

func historyCreatedAt(id string) int64 {
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0
	}
	if len(id) < 13 {
		value *= 1000
	}
	return value
}

func historyMetadataPath(id string) string {
	return filepath.Join(ModDir, "history", id+".json")
}

func writeHistoryMetadata(id string, meta historyMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	path := historyMetadataPath(id)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func ensureHistoryMetadata(id string) (historyMetadata, error) {
	path := historyMetadataPath(id)
	if data, err := os.ReadFile(path); err == nil {
		var meta historyMetadata
		if json.Unmarshal(data, &meta) == nil && meta.Version == historyMetadataVersion && meta.SizeBytes >= 0 && meta.Width > 0 && meta.Height > 0 && meta.FPS > 0 {
			return meta, nil
		}
	}

	zipPath := filepath.Join(ModDir, "history", id+".zip")
	meta, err := inspectBootAnimation(zipPath, id)
	if err != nil {
		return historyMetadata{}, err
	}
	if err := writeHistoryMetadata(id, meta); err != nil {
		writeLog("Warning: Could not cache history metadata for ID: " + id)
	}
	return meta, nil
}

func historyPreviewState(id string) (bool, string) {
	for _, candidate := range []struct {
		ext  string
		kind string
	}{
		{".webm", "webm"},
		{".gif", "gif"},
	} {
		path := filepath.Join(ModDir, "history", id+candidate.ext)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
			return true, candidate.kind
		}
	}
	return false, ""
}

func resizeNearest(source image.Image, maxWidth, maxHeight int) image.Image {
	bounds := source.Bounds()
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return source
	}
	targetWidth := sourceWidth
	targetHeight := sourceHeight
	if targetWidth > maxWidth {
		targetHeight = targetHeight * maxWidth / targetWidth
		targetWidth = maxWidth
	}
	if targetHeight > maxHeight {
		targetWidth = targetWidth * maxHeight / targetHeight
		targetHeight = maxHeight
	}
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}
	if targetWidth == sourceWidth && targetHeight == sourceHeight {
		return source
	}

	output := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		sy := bounds.Min.Y + y*sourceHeight/targetHeight
		for x := 0; x < targetWidth; x++ {
			sx := bounds.Min.X + x*sourceWidth/targetWidth
			output.Set(x, y, source.At(sx, sy))
		}
	}
	return output
}

func generateHistoryGIF(zipPath, outputPath string) error {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer archive.Close()
	if err := auditBootArchiveEntries(archive.File); err != nil {
		return err
	}

	frames := make([]*zip.File, 0, 32)
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name)
		if strings.Contains(entry.Name, "/") && (strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg")) {
			frames = append(frames, entry)
		}
	}
	if len(frames) == 0 {
		return errors.New("no previewable frames")
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].Name < frames[j].Name })

	const maxFrames = 8
	sampleCount := len(frames)
	if sampleCount > maxFrames {
		sampleCount = maxFrames
	}
	animation := &gif.GIF{LoopCount: 0}
	for i := 0; i < sampleCount; i++ {
		index := 0
		if sampleCount > 1 {
			index = i * (len(frames) - 1) / (sampleCount - 1)
		}
		entry := frames[index]

		configReader, err := entry.Open()
		if err != nil {
			continue
		}
		config, _, configErr := image.DecodeConfig(configReader)
		_ = configReader.Close()
		if configErr != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 24_000_000 {
			continue
		}

		reader, err := entry.Open()
		if err != nil {
			continue
		}
		decoded, _, decodeErr := image.Decode(reader)
		_ = reader.Close()
		if decodeErr != nil {
			continue
		}
		small := resizeNearest(decoded, 180, 240)
		paletted := image.NewPaletted(image.Rect(0, 0, small.Bounds().Dx(), small.Bounds().Dy()), palette.Plan9)
		draw.Draw(paletted, paletted.Rect, small, small.Bounds().Min, draw.Src)
		animation.Image = append(animation.Image, paletted)
		animation.Delay = append(animation.Delay, 8)
	}
	if len(animation.Image) == 0 {
		return errors.New("could not decode preview frames")
	}

	temp := outputPath + ".tmp"
	output, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	encodeErr := gif.EncodeAll(output, animation)
	closeErr := output.Close()
	if encodeErr != nil {
		_ = os.Remove(temp)
		return encodeErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	return os.Rename(temp, outputPath)
}

func validHistoryID(id string) bool {
	return historyIDPattern.MatchString(id)
}

func historyListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	historyDir := ModDir + "/history"
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read history"})
		return
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".zip")
		if validHistoryID(id) {
			ids = append(ids, id)
		}
	}

	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	writeJSON(w, http.StatusOK, ids)
}

func historyItemsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	historyDir := filepath.Join(ModDir, "history")
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []historyItemResponse{})
			return
		}
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read history"})
		return
	}

	ids := make([]string, 0, historyLimit)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".zip")
		if validHistoryID(id) {
			ids = append(ids, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))

	items := make([]historyItemResponse, 0, len(ids))
	for _, id := range ids {
		meta, err := ensureHistoryMetadata(id)
		if err != nil {
			writeLog("Warning: Could not inspect history item (ID: " + id + "): " + err.Error())
			meta = historyMetadata{Version: historyMetadataVersion, CreatedAt: historyCreatedAt(id)}
			if info, statErr := os.Stat(filepath.Join(historyDir, id+".zip")); statErr == nil && !info.IsDir() {
				meta.SizeBytes = info.Size()
				if meta.CreatedAt == 0 {
					meta.CreatedAt = info.ModTime().UnixMilli()
				}
			}
		}
		hasPreview, previewType := historyPreviewState(id)
		items = append(items, historyItemResponse{
			ID:          id,
			CreatedAt:   meta.CreatedAt,
			SizeBytes:   meta.SizeBytes,
			Width:       meta.Width,
			Height:      meta.Height,
			FPS:         meta.FPS,
			FrameCount:  meta.FrameCount,
			Parts:       meta.Parts,
			FrameFormat: meta.FrameFormat,
			HasAudio:    meta.HasAudio,
			HasPreview:  hasPreview,
			PreviewType: previewType,
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func historyDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		http.Error(w, "Invalid history ID", http.StatusBadRequest)
		return
	}
	path := filepath.Join(ModDir, "history", id+".zip")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.Error(w, "History item not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=bootanimation-"+id+".zip")
	http.ServeFile(w, r, path)
}

func historyPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		http.Error(w, "Invalid history ID", http.StatusBadRequest)
		return
	}

	previewPath := filepath.Join(ModDir, "history", id+".webm")
	contentType := "video/webm"
	if info, err := os.Stat(previewPath); err != nil || info.IsDir() || info.Size() == 0 {
		previewPath = filepath.Join(ModDir, "history", id+".gif")
		contentType = "image/gif"
		if info, err = os.Stat(previewPath); err != nil || info.IsDir() || info.Size() == 0 {
			historyPreviewMu.Lock()
			info, statErr := os.Stat(previewPath)
			if statErr != nil || info.IsDir() || info.Size() == 0 {
				zipPath := filepath.Join(ModDir, "history", id+".zip")
				if err := generateHistoryGIF(zipPath, previewPath); err != nil {
					historyPreviewMu.Unlock()
					writeLog("Warning: Could not generate history preview for ID: " + id + ": " + err.Error())
					http.Error(w, "Preview not available", http.StatusNotFound)
					return
				}
			}
			historyPreviewMu.Unlock()
		}
	}

	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, previewPath)
}

func historyApplyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid history ID"})
		return
	}
	historyPath := filepath.Join(ModDir, "history", id+".zip")
	if info, err := os.Stat(historyPath); err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "History item not found"})
		return
	}
	if err := validateBootAnimation(historyPath); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "History item is invalid: " + err.Error()})
		writeLog("Error: Refused to restore invalid history item (ID: " + id + ").")
		return
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", historyPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error injecting history"})
		writeLog("Error: Failed to restore animation from history (ID: " + id + "): " + strings.TrimSpace(string(output)))
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Animation restored from history"})
	showToastEvent("toast_history_restored", "⏳ Boot Creator: Past animation restored successfully!")
	writeLog("Success: Animation restored from history (ID: " + id + ").")
}

func historyDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid history ID"})
		return
	}

	zipPath := filepath.Join(ModDir, "history", id+".zip")
	previewWebMPath := filepath.Join(ModDir, "history", id+".webm")
	previewLegacyPath := filepath.Join(ModDir, "history", id+".gif")
	metadataPath := historyMetadataPath(id)
	removed := false

	if err := os.Remove(zipPath); err == nil {
		removed = true
	} else if !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not delete history item"})
		return
	}

	for _, previewPath := range []string{previewWebMPath, previewLegacyPath, metadataPath} {
		if err := os.Remove(previewPath); err != nil && !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not delete history preview"})
			return
		}
	}

	if !removed {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "History item not found"})
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_history_deleted", "🧹 Boot Creator: An old animation was deleted from history.")
	writeLog("History item deleted manually by user (ID: " + id + ").")
}
