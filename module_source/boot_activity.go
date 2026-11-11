package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type bootActivityEntry struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	EntryID         string `json:"entry_id,omitempty"`
	ObjectID        string `json:"object_id,omitempty"`
	Name            string `json:"name,omitempty"`
	Source          string `json:"source,omitempty"`
	Reason          string `json:"reason,omitempty"`
	PreparedAt      int64  `json:"prepared_at,omitempty"`
	BootCompletedAt int64  `json:"boot_completed_at,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	Message         string `json:"message,omitempty"`
}

type bootActivityState struct {
	Version int                 `json:"version"`
	Items   []bootActivityEntry `json:"items"`
}

type bootActivityResponse struct {
	Status string              `json:"status"`
	Limit  int                 `json:"limit"`
	Items  []bootActivityEntry `json:"items"`
}

func bootActivityStatePath() string {
	return filepath.Join(playlistRootDir(), "boot_activity.json")
}

func loadBootActivityStateLocked() (bootActivityState, error) {
	if err := os.MkdirAll(playlistRootDir(), 0755); err != nil {
		return bootActivityState{}, err
	}
	data, err := os.ReadFile(bootActivityStatePath())
	if os.IsNotExist(err) {
		return bootActivityState{Version: bootActivityStateVersion, Items: []bootActivityEntry{}}, nil
	}
	if err != nil {
		return bootActivityState{}, err
	}
	var state bootActivityState
	if json.Unmarshal(data, &state) != nil || state.Version != bootActivityStateVersion {
		return bootActivityState{}, errors.New("invalid boot activity state")
	}
	if state.Items == nil {
		state.Items = []bootActivityEntry{}
	}
	return state, nil
}

func saveBootActivityStateLocked(state bootActivityState) error {
	if err := os.MkdirAll(playlistRootDir(), 0755); err != nil {
		return err
	}
	state.Version = bootActivityStateVersion
	if len(state.Items) > bootActivityLimit {
		state.Items = append([]bootActivityEntry(nil), state.Items[len(state.Items)-bootActivityLimit:]...)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temp := bootActivityStatePath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, bootActivityStatePath())
}

func appendBootActivity(entry bootActivityEntry) {
	bootActivityMu.Lock()
	defer bootActivityMu.Unlock()
	state, err := loadBootActivityStateLocked()
	if err != nil {
		writeLog("Boot Activity could not read its state: " + err.Error())
		return
	}
	now := time.Now().UnixMilli()
	if entry.CreatedAt <= 0 {
		entry.CreatedAt = now
	}
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("%d-%06d", now, rotationRandomIndex(1000000))
	}
	state.Items = append(state.Items, entry)
	if err := saveBootActivityStateLocked(state); err != nil {
		writeLog("Boot Activity could not save its state: " + err.Error())
	}
}

func recordBootActivityError(name, source, reason string, err error) {
	if err == nil {
		return
	}
	appendBootActivity(bootActivityEntry{
		Kind:      "diagnostic",
		Status:    "error",
		Name:      cleanPlaylistName(name, "Boot animation"),
		Source:    normalizeNextSource(source),
		Reason:    strings.TrimSpace(reason),
		CreatedAt: time.Now().UnixMilli(),
		Message:   err.Error(),
	})
}

func recordBootActivityPrepared(entryID, objectID, name, source, reason string, preparedAt int64) {
	appendBootActivity(bootActivityEntry{
		Kind:       "prepared",
		Status:     "prepared",
		EntryID:    entryID,
		ObjectID:   objectID,
		Name:       cleanPlaylistName(name, "Boot animation"),
		Source:     normalizeNextSource(source),
		Reason:     strings.TrimSpace(reason),
		PreparedAt: preparedAt,
		CreatedAt:  preparedAt,
	})
}

func bootActivityListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	bootActivityMu.Lock()
	defer bootActivityMu.Unlock()
	state, err := loadBootActivityStateLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read Boot Activity"})
		return
	}
	items := append([]bootActivityEntry(nil), state.Items...)
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	writeJSON(w, http.StatusOK, bootActivityResponse{Status: "success", Limit: bootActivityLimit, Items: items})
}

func bootActivityClearHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	bootActivityMu.Lock()
	defer bootActivityMu.Unlock()
	if err := saveBootActivityStateLocked(bootActivityState{Version: bootActivityStateVersion, Items: []bootActivityEntry{}}); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not clear Boot Activity"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToastEvent("toast_boot_activity_cleared", "🧹 Boot Creator: Boot Activity was cleared.")
	writeLog("Boot Activity cleared by user.")
}
