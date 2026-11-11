package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type bootQueueMutationRequest struct {
	Source    string   `json:"source"`
	ObjectID  string   `json:"object_id"`
	HistoryID string   `json:"history_id"`
	Name      string   `json:"name"`
	ID        string   `json:"id"`
	IDs       []string `json:"ids"`
}

func decodeBootQueueMutation(w http.ResponseWriter, r *http.Request) (bootQueueMutationRequest, bool) {
	var request bootQueueMutationRequest
	if err := decodeJSONRequest(r, 64<<10, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid Boot Queue request"})
		return bootQueueMutationRequest{}, false
	}
	return request, true
}

func resolveBootQueueSource(request bootQueueMutationRequest) (bootQueueItem, error) {
	source := strings.ToLower(strings.TrimSpace(request.Source))
	name := strings.TrimSpace(request.Name)
	objectID := ""
	switch source {
	case "playlist":
		objectID = strings.TrimSpace(request.ObjectID)
		if !playlistObjectPattern.MatchString(objectID) {
			return bootQueueItem{}, errors.New("invalid playlist animation")
		}
		if info, err := os.Stat(playlistObjectZipPath(objectID)); err != nil || info.IsDir() || info.Size() <= 0 {
			return bootQueueItem{}, errors.New("playlist animation not found")
		}
	case "history":
		historyID := strings.TrimSpace(request.HistoryID)
		if !historyIDPattern.MatchString(historyID) {
			return bootQueueItem{}, errors.New("invalid History animation")
		}
		sourcePath := filepath.Join(ModDir, "history", historyID+".zip")
		if info, err := os.Stat(sourcePath); err != nil || info.IsDir() {
			return bootQueueItem{}, errors.New("History animation not found")
		}
		playlistMu.Lock()
		var err error
		objectID, _, err = ensurePlaylistObject(sourcePath)
		playlistMu.Unlock()
		if err != nil {
			return bootQueueItem{}, err
		}
	case "staged":
		sourcePath := stagedAnimationPath()
		if err := validateBootAnimation(sourcePath); err != nil {
			return bootQueueItem{}, errors.New("no valid staged animation")
		}
		playlistMu.Lock()
		var err error
		objectID, _, err = ensurePlaylistObject(sourcePath)
		playlistMu.Unlock()
		if err != nil {
			return bootQueueItem{}, err
		}
	default:
		return bootQueueItem{}, errors.New("unsupported Boot Queue source")
	}
	id, err := newPlaylistIdentifier("q_")
	if err != nil {
		return bootQueueItem{}, err
	}
	return bootQueueItem{ID: id, ObjectID: objectID, Name: cleanPlaylistName(name, "Boot animation"), Origin: source, AddedAt: time.Now().UnixMilli()}, nil
}

func garbageCollectAutomationObjectsLocked() {
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err == nil {
		garbageCollectPlaylistObjectsLocked(state)
	}
}

func bootQueueAddHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodeBootQueueMutation(w, r)
	if !ok {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	item, err := resolveBootQueueSource(request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	state.Queue = append(state.Queue, item)
	state.LastError = ""
	if state.Override == nil && state.NextSource != "queue" {
		if err := prepareQueueItemLocked(&state, state.Queue[0], "queue", "queue-added"); err != nil {
			state.Queue = state.Queue[:len(state.Queue)-1]
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			garbageCollectAutomationObjectsLocked()
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save Boot Queue"})
		return
	}
	writeLog("Animation added to Boot Queue: " + item.Name)
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_added", "📥 Boot Creator: Animation added to Boot Queue.")
}

func bootQueueUseNextHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodeBootQueueMutation(w, r)
	if !ok {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	item, err := resolveBootQueueSource(request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	state.Override = &item
	if err := prepareQueueItemLocked(&state, item, "override", "use-next-boot"); err != nil {
		state.Override = nil
		state.LastError = err.Error()
		_ = saveRotationStateLocked(state)
		garbageCollectAutomationObjectsLocked()
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	garbageCollectAutomationObjectsLocked()
	writeLog("Next boot override prepared: " + item.Name)
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_next", "1️⃣ Boot Creator: One-time next boot prepared.")
}

func bootQueueReorderHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodeBootQueueMutation(w, r)
	if !ok {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	if len(request.IDs) != len(state.Queue) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid Boot Queue order"})
		return
	}
	lookup := make(map[string]bootQueueItem, len(state.Queue))
	for _, item := range state.Queue {
		lookup[item.ID] = item
	}
	seen := make(map[string]struct{}, len(request.IDs))
	reordered := make([]bootQueueItem, 0, len(state.Queue))
	for _, id := range request.IDs {
		item, exists := lookup[id]
		if !exists {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid Boot Queue order"})
			return
		}
		if _, duplicate := seen[id]; duplicate {
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid Boot Queue order"})
			return
		}
		seen[id] = struct{}{}
		reordered = append(reordered, item)
	}
	originalQueue := append([]bootQueueItem(nil), state.Queue...)
	state.Queue = reordered
	if state.Override == nil && len(state.Queue) > 0 && (state.NextSource != "queue" || state.NextQueueID != state.Queue[0].ID) {
		if err := prepareQueueItemLocked(&state, state.Queue[0], "queue", "queue-reordered"); err != nil {
			state.Queue = originalQueue
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save Boot Queue"})
		return
	}
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
}

func bootQueueRemoveHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	request, ok := decodeBootQueueMutation(w, r)
	if !ok {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	index := -1
	for i, item := range state.Queue {
		if item.ID == request.ID {
			index = i
			break
		}
	}
	if index < 0 {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Boot Queue item not found"})
		return
	}
	wasNext := state.NextSource == "queue" && state.NextQueueID == state.Queue[index].ID
	state.Queue = append(state.Queue[:index], state.Queue[index+1:]...)
	if wasNext {
		clearPreparedNext(&state)
		if err := prepareAutomationNextLocked(&state, "queue-remove"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save Boot Queue"})
		return
	}
	garbageCollectAutomationObjectsLocked()
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_removed", "📤 Boot Creator: Animation removed from Boot Queue.")
}

func bootQueueClearHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	wasQueueNext := state.NextSource == "queue"
	state.Queue = []bootQueueItem{}
	if wasQueueNext {
		clearPreparedNext(&state)
		if err := prepareAutomationNextLocked(&state, "queue-cleared"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save Boot Queue"})
		return
	}
	garbageCollectAutomationObjectsLocked()
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_cleared", "🧹 Boot Creator: Boot Queue cleared.")
}

func bootQueueSkipNextHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Queue"})
		return
	}
	switch state.NextSource {
	case "override":
		state.Override = nil
		clearPreparedNext(&state)
		if err := prepareAutomationNextLocked(&state, "override-skipped"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	case "queue":
		index := -1
		for i, item := range state.Queue {
			if item.ID == state.NextQueueID {
				index = i
				break
			}
		}
		if index >= 0 {
			state.Queue = append(state.Queue[:index], state.Queue[index+1:]...)
		}
		clearPreparedNext(&state)
		if err := prepareAutomationNextLocked(&state, "queue-skipped"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	case "rotation":
		if !state.Enabled {
			writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Boot rotation is disabled"})
			return
		}
		reference := state.NextEntryID
		wasPaused := state.Paused
		state.Paused = false
		if err := prepareRotationNextLocked(&state, reference, "skip-next"); err != nil {
			state.Paused = wasPaused
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
		state.Paused = wasPaused
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
	default:
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Nothing is prepared for the next boot"})
		return
	}
	garbageCollectAutomationObjectsLocked()
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_queue_skipped", "⏭️ Boot Creator: Prepared next boot skipped.")
}
