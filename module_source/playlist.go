package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type playlistItemResponse struct {
	ID          string `json:"id"`
	ObjectID    string `json:"object_id"`
	Name        string `json:"name"`
	AddedAt     int64  `json:"added_at"`
	SizeBytes   int64  `json:"size_bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	FrameCount  int    `json:"frame_count"`
	Parts       int    `json:"parts"`
	FrameFormat string `json:"frame_format"`
	HasAudio    bool   `json:"has_audio"`
	HasPreview  bool   `json:"has_preview"`
}

type playlistResponse struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	CreatedAt int64                  `json:"created_at"`
	UpdatedAt int64                  `json:"updated_at"`
	Items     []playlistItemResponse `json:"items"`
}

type playlistListResponse struct {
	Status    string             `json:"status"`
	Playlists []playlistResponse `json:"playlists"`
}

type playlistMutationRequest struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	PlaylistID string   `json:"playlist_id"`
	EntryID    string   `json:"entry_id"`
	IDs        []string `json:"ids"`
}

func playlistListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	result := playlistListResponse{Status: "success", Playlists: make([]playlistResponse, 0, len(state.Playlists))}
	for _, playlist := range state.Playlists {
		result.Playlists = append(result.Playlists, playlistResponseLocked(playlist))
	}
	writeJSON(w, http.StatusOK, result)
}

func playlistCreateHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	id, err := newPlaylistIdentifier("pl_")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create playlist"})
		return
	}
	now := time.Now().UnixMilli()
	record := playlistRecord{ID: id, Name: cleanPlaylistName(request.Name, "New playlist"), CreatedAt: now, UpdatedAt: now, Items: []playlistItemRef{}}
	state.Playlists = append(state.Playlists, record)
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	writeJSON(w, http.StatusOK, playlistResponseLocked(record))
	showToastEvent("toast_playlist_created", "📚 Boot Creator: Playlist created.")
	writeLog("Playlist created: " + record.Name)
}

func playlistRenameHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok || !playlistIDPattern.MatchString(request.ID) {
		if ok {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		}
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	playlist, _ := findPlaylistLocked(&state, request.ID)
	if playlist == nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	playlist.Name = cleanPlaylistName(request.Name, playlist.Name)
	playlist.UpdatedAt = time.Now().UnixMilli()
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_playlist_renamed", "📚 Boot Creator: Playlist renamed.")
}

func playlistDuplicateHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok || !playlistIDPattern.MatchString(request.ID) {
		if ok {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		}
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	source, _ := findPlaylistLocked(&state, request.ID)
	if source == nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	newID, err := newPlaylistIdentifier("pl_")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not duplicate playlist"})
		return
	}
	now := time.Now().UnixMilli()
	copyRecord := playlistRecord{ID: newID, Name: cleanPlaylistName(request.Name, source.Name+" copy"), CreatedAt: now, UpdatedAt: now, Items: make([]playlistItemRef, 0, len(source.Items))}
	for _, sourceItem := range source.Items {
		entryID, idErr := newPlaylistIdentifier("it_")
		if idErr != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not duplicate playlist"})
			return
		}
		copyRecord.Items = append(copyRecord.Items, playlistItemRef{ID: entryID, ObjectID: sourceItem.ObjectID, Name: sourceItem.Name, AddedAt: now})
	}
	state.Playlists = append(state.Playlists, copyRecord)
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	writeJSON(w, http.StatusOK, playlistResponseLocked(copyRecord))
	showToastEvent("toast_playlist_duplicated", "📚 Boot Creator: Playlist duplicated.")
}

func playlistDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok || !playlistIDPattern.MatchString(request.ID) {
		if ok {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		}
		return
	}
	playlistMu.Lock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		playlistMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	_, index := findPlaylistLocked(&state, request.ID)
	if index < 0 {
		playlistMu.Unlock()
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	state.Playlists = append(state.Playlists[:index], state.Playlists[index+1:]...)
	if err := savePlaylistStateLocked(state); err != nil {
		playlistMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	garbageCollectPlaylistObjectsLocked(state)
	playlistMu.Unlock()
	disableRotationForDeletedPlaylist(request.ID)
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_playlist_deleted", "📚 Boot Creator: Playlist deleted.")
}

func playlistReorderHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	if len(request.IDs) != len(state.Playlists) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist order"})
		return
	}
	lookup := make(map[string]playlistRecord, len(state.Playlists))
	for _, playlist := range state.Playlists {
		lookup[playlist.ID] = playlist
	}
	reordered := make([]playlistRecord, 0, len(state.Playlists))
	seen := make(map[string]struct{}, len(request.IDs))
	for _, id := range request.IDs {
		playlist, exists := lookup[id]
		if !exists {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist order"})
			return
		}
		if _, duplicate := seen[id]; duplicate {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist order"})
			return
		}
		seen[id] = struct{}{}
		reordered = append(reordered, playlist)
	}
	state.Playlists = reordered
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist order"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
}

func playlistAddSourceLocked(state *playlistState, playlistID, sourcePath, name string) (playlistItemRef, error) {
	if playlist, _ := findPlaylistLocked(state, playlistID); playlist == nil {
		return playlistItemRef{}, errors.New("playlist not found")
	}
	objectID, _, err := ensurePlaylistObject(sourcePath)
	if err != nil {
		return playlistItemRef{}, err
	}
	item, err := addPlaylistItemLocked(state, playlistID, objectID, name)
	if err != nil {
		return playlistItemRef{}, err
	}
	if err := savePlaylistStateLocked(*state); err != nil {
		if diskState, loadErr := loadPlaylistStateLocked(); loadErr == nil {
			garbageCollectPlaylistObjectsLocked(diskState)
		}
		return playlistItemRef{}, err
	}
	return item, nil
}

func playlistUploadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBootAnimationBytes+(8<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid multipart upload"})
		return
	}
	temp, err := os.CreateTemp("/data/local/tmp", "boot_creator_playlist_*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare upload"})
		return
	}
	tempPath := temp.Name()
	_ = temp.Close()
	defer os.Remove(tempPath)
	playlistID := ""
	name := ""
	hasAnimation := false
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Could not read multipart upload"})
			return
		}
		switch part.FormName() {
		case "playlist_id":
			data, _ := io.ReadAll(io.LimitReader(part, 256))
			playlistID = strings.TrimSpace(string(data))
		case "name":
			data, _ := io.ReadAll(io.LimitReader(part, 512))
			name = strings.TrimSpace(string(data))
		case "bootanimation":
			if !hasAnimation {
				if err := saveMultipartFile(part, tempPath, maxBootAnimationBytes); err != nil {
					_ = part.Close()
					writeJSON(w, http.StatusRequestEntityTooLarge, statusResponse{Status: "error", Message: "bootanimation.zip exceeds the transport safety limit"})
					return
				}
				hasAnimation = true
			}
		}
		_ = part.Close()
	}
	if !playlistIDPattern.MatchString(playlistID) || !hasAnimation {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Missing playlist or animation"})
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	item, err := playlistAddSourceLocked(&state, playlistID, tempPath, name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
	showToastEvent("toast_playlist_item_added", "📚 Boot Creator: Animation added to playlist.")
	writeLog("Animation added to playlist from upload.")
}

func playlistAddHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	historyID := strings.TrimSpace(request.EntryID)
	if !playlistIDPattern.MatchString(request.PlaylistID) || !historyIDPattern.MatchString(historyID) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist or history item"})
		return
	}
	source := filepath.Join(ModDir, "history", historyID+".zip")
	if info, err := os.Stat(source); err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "History item not found"})
		return
	}
	name := request.Name
	if name == "" {
		name = "History " + historyID
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	item, err := playlistAddSourceLocked(&state, request.PlaylistID, source, name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
	showToastEvent("toast_playlist_item_added", "📚 Boot Creator: Animation added to playlist.")
}

func playlistAddStagedHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	if !playlistIDPattern.MatchString(request.PlaylistID) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		return
	}
	source := stagedAnimationPath()
	if err := validateBootAnimation(source); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "No valid staged animation"})
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	item, err := playlistAddSourceLocked(&state, request.PlaylistID, source, request.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
	showToastEvent("toast_playlist_item_added", "📚 Boot Creator: Animation added to playlist.")
}

func playlistItemRemoveHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	if !playlistIDPattern.MatchString(request.PlaylistID) || !playlistIDPattern.MatchString(request.EntryID) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist item"})
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	playlist, _ := findPlaylistLocked(&state, request.PlaylistID)
	if playlist == nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	index := -1
	for i, item := range playlist.Items {
		if item.ID == request.EntryID {
			index = i
			break
		}
	}
	if index < 0 {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist item not found"})
		return
	}
	playlist.Items = append(playlist.Items[:index], playlist.Items[index+1:]...)
	playlist.UpdatedAt = time.Now().UnixMilli()
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save playlist"})
		return
	}
	garbageCollectPlaylistObjectsLocked(state)
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_playlist_item_removed", "📚 Boot Creator: Animation removed from playlist.")
}

func playlistItemReorderHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodePlaylistMutation(w, r)
	if !ok {
		return
	}
	if !playlistIDPattern.MatchString(request.PlaylistID) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid playlist"})
		return
	}
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	playlist, _ := findPlaylistLocked(&state, request.PlaylistID)
	if playlist == nil {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Playlist not found"})
		return
	}
	if len(request.IDs) != len(playlist.Items) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid item order"})
		return
	}
	lookup := make(map[string]playlistItemRef, len(playlist.Items))
	for _, item := range playlist.Items {
		lookup[item.ID] = item
	}
	reordered := make([]playlistItemRef, 0, len(playlist.Items))
	seen := make(map[string]struct{}, len(request.IDs))
	for _, id := range request.IDs {
		item, exists := lookup[id]
		if !exists {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid item order"})
			return
		}
		if _, duplicate := seen[id]; duplicate {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid item order"})
			return
		}
		seen[id] = struct{}{}
		reordered = append(reordered, item)
	}
	playlist.Items = reordered
	playlist.UpdatedAt = time.Now().UnixMilli()
	if err := savePlaylistStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save item order"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
}

func playlistPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	objectID := strings.TrimSpace(r.URL.Query().Get("object"))
	if !playlistObjectPattern.MatchString(objectID) {
		http.Error(w, "Invalid playlist object", http.StatusBadRequest)
		return
	}
	zipPath := playlistObjectZipPath(objectID)
	if info, err := os.Stat(zipPath); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	previewPath := playlistObjectPreviewPath(objectID)
	if info, err := os.Stat(previewPath); err != nil || info.IsDir() || info.Size() == 0 {
		playlistPreviewMu.Lock()
		info, statErr := os.Stat(previewPath)
		if statErr != nil || info.IsDir() || info.Size() == 0 {
			if err := generateHistoryGIF(zipPath, previewPath); err != nil {
				playlistPreviewMu.Unlock()
				http.Error(w, "Preview unavailable", http.StatusUnprocessableEntity)
				return
			}
		}
		playlistPreviewMu.Unlock()
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, previewPath)
}

func playlistDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	objectID := strings.TrimSpace(r.URL.Query().Get("object"))
	if !playlistObjectPattern.MatchString(objectID) {
		http.Error(w, "Invalid playlist object", http.StatusBadRequest)
		return
	}
	path := playlistObjectZipPath(objectID)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="bootanimation.zip"`)
	http.ServeFile(w, r, path)
}

func applyPlaylistObject(objectID string) (uploadResponse, error) {
	path := playlistObjectZipPath(objectID)
	if !playlistObjectPattern.MatchString(objectID) {
		return uploadResponse{}, errors.New("invalid playlist object")
	}
	if err := validateBootAnimation(path); err != nil {
		return uploadResponse{}, errors.New("playlist animation is invalid")
	}
	historyDir := filepath.Join(ModDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return uploadResponse{}, err
	}
	id := newHistoryID(historyDir)
	historyPath := filepath.Join(historyDir, id+".zip")
	if err := linkOrCopyFile(path, historyPath, 0644); err != nil {
		return uploadResponse{}, err
	}
	meta, err := inspectBootAnimation(historyPath, id)
	if err != nil {
		_ = os.Remove(historyPath)
		return uploadResponse{}, err
	}
	_ = writeHistoryMetadata(id, meta)
	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", historyPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(historyPath)
		_ = os.Remove(historyMetadataPath(id))
		return uploadResponse{}, errors.New(strings.TrimSpace(string(output)))
	}
	cleanHistory(historyDir)
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	return uploadResponse{Status: "success", HistoryID: id, SizeBytes: meta.SizeBytes, Warnings: warnings}, nil
}

func playlistApplyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	objectID := strings.TrimSpace(r.URL.Query().Get("object"))
	result, err := applyPlaylistObject(objectID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
	showToastEvent("toast_playlist_applied", "Boot Animation Studio: Playlist animation applied.")
	writeLog("Playlist animation applied and saved to history.")
}

func stagePlaylistObject(objectID string) (historyMetadata, error) {
	if !playlistObjectPattern.MatchString(objectID) {
		return historyMetadata{}, errors.New("invalid playlist object")
	}
	source := playlistObjectZipPath(objectID)
	meta, err := inspectBootAnimation(source, "")
	if err != nil {
		return historyMetadata{}, err
	}
	if err := os.MkdirAll(filepath.Dir(stagedAnimationPath()), 0755); err != nil {
		return historyMetadata{}, err
	}
	temp := stagedAnimationPath() + ".tmp"
	_ = os.Remove(temp)
	if err := linkOrCopyFile(source, temp, 0644); err != nil {
		return historyMetadata{}, err
	}
	if err := setSystemFileContext(temp); err != nil {
		_ = os.Remove(temp)
		return historyMetadata{}, err
	}
	_ = os.Remove(stagedAnimationPath())
	if err := os.Rename(temp, stagedAnimationPath()); err != nil {
		_ = os.Remove(temp)
		return historyMetadata{}, err
	}
	return meta, nil
}

func playlistStageHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	objectID := strings.TrimSpace(r.URL.Query().Get("object"))
	meta, err := stagePlaylistObject(objectID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	warnings := []string{}
	if meta.SizeBytes > directUploadWarningBytes {
		warnings = append(warnings, "large_boot_animation")
	}
	writeJSON(w, http.StatusOK, testStageResponse{Status: "success", HasStaged: true, PreviewActive: false, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, FPS: meta.FPS, FrameCount: meta.FrameCount, Parts: meta.Parts, FrameFormat: meta.FrameFormat, HasAudio: meta.HasAudio, Warnings: warnings})
}
