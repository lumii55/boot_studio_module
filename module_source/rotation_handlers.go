package main

import (
	"net/http"
	"strings"
)

type rotationConfigureRequest struct {
	Enabled    bool   `json:"enabled"`
	PlaylistID string `json:"playlist_id"`
	Mode       string `json:"mode"`
}

type rotationPauseRequest struct {
	Paused bool `json:"paused"`
}

func rotationStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read rotation state"})
		return
	}
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
}

func rotationConfigureHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	var request rotationConfigureRequest
	if err := decodeJSONRequest(r, 64<<10, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid rotation request"})
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read rotation state"})
		return
	}
	if !request.Enabled {
		state.Enabled = false
		state.Paused = false
		state.ShuffleRemaining = []string{}
		state.LastError = ""
		if state.NextSource == "rotation" {
			clearPreparedNext(&state)
		}
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
		if state.Override != nil || len(state.Queue) > 0 {
			if err := prepareAutomationNextLocked(&state, "rotation-disabled"); err != nil {
				state.LastError = err.Error()
				_ = saveRotationStateLocked(state)
			}
		}
		writeLog("Boot rotation disabled. Queue and one-shot controls were preserved.")
		writeJSON(w, http.StatusOK, rotationResponseLocked(state))
		showToastEvent("toast_rotation_disabled", "🔄 Boot Creator: Boot Rotation disabled.")
		return
	}
	playlistID := strings.TrimSpace(request.PlaylistID)
	mode := normalizeRotationMode(request.Mode)
	playlist, found, err := rotationPlaylistSnapshot(playlistID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read playlists"})
		return
	}
	if !found || len(rotationUsableItems(playlist)) == 0 {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Choose a playlist that contains at least one animation"})
		return
	}
	changed := !state.Enabled || state.PlaylistID != playlistID || normalizeRotationMode(state.Mode) != mode
	state.Enabled = true
	state.PlaylistID = playlistID
	state.Mode = mode
	state.LastError = ""
	if changed {
		state.ShuffleRemaining = []string{}
		if state.NextSource == "rotation" {
			clearPreparedNext(&state)
		}
	}
	if state.Override != nil || len(state.Queue) > 0 {
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
		writeJSON(w, http.StatusOK, rotationResponseLocked(state))
		showToastEvent("toast_rotation_configured", "🔄 Boot Creator: Boot Rotation settings updated.")
		return
	}
	if !changed && state.NextSource == "rotation" && state.NextEntryID != "" {
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
		writeJSON(w, http.StatusOK, rotationResponseLocked(state))
		showToastEvent("toast_rotation_configured", "🔄 Boot Creator: Boot Rotation settings updated.")
		return
	}
	if state.Paused {
		if err := saveRotationStateLocked(state); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
			return
		}
		writeJSON(w, http.StatusOK, rotationResponseLocked(state))
		showToastEvent("toast_rotation_configured", "🔄 Boot Creator: Boot Rotation settings updated.")
		return
	}
	if err := prepareRotationNextLocked(&state, state.LastRotationEntryID, "configuration"); err != nil {
		state.LastError = err.Error()
		_ = saveRotationStateLocked(state)
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_rotation_configured", "🔄 Boot Creator: Boot Rotation settings updated.")
}

func rotationPrepareHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read rotation state"})
		return
	}
	if !state.Enabled {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Boot rotation is disabled"})
		return
	}
	if state.Paused {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Boot rotation is paused"})
		return
	}
	if state.NextSource != "" && state.NextSource != "rotation" {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "The next boot is controlled by Boot Queue"})
		return
	}
	reference := state.NextEntryID
	if reference == "" {
		reference = state.LastRotationEntryID
	}
	if err := prepareRotationNextLocked(&state, reference, "manual"); err != nil {
		state.LastError = err.Error()
		_ = saveRotationStateLocked(state)
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	showToastEvent("toast_rotation_prepared", "🔄 Boot Creator: Next Rotation animation prepared.")
}

func rotationPauseHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	var request rotationPauseRequest
	if err := decodeJSONRequest(r, 64<<10, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid pause request"})
		return
	}
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read rotation state"})
		return
	}
	if !state.Enabled {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Boot rotation is disabled"})
		return
	}
	state.Paused = request.Paused
	state.LastError = ""
	if !state.Paused && state.Override == nil && len(state.Queue) == 0 && state.NextSource == "" {
		if err := prepareRotationNextLocked(&state, state.LastRotationEntryID, "resume"); err != nil {
			state.LastError = err.Error()
			_ = saveRotationStateLocked(state)
			writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
			return
		}
	} else if err := saveRotationStateLocked(state); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save rotation state"})
		return
	}
	writeLog("Boot rotation pause state changed.")
	writeJSON(w, http.StatusOK, rotationResponseLocked(state))
	if state.Paused {
		showToastEvent("toast_rotation_paused", "⏸️ Boot Creator: Boot Rotation paused.")
	} else {
		showToastEvent("toast_rotation_resumed", "▶️ Boot Creator: Boot Rotation resumed.")
	}
}
