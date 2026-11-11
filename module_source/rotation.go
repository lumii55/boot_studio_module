package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type bootQueueItem struct {
	ID       string `json:"id"`
	ObjectID string `json:"object_id"`
	Name     string `json:"name"`
	Origin   string `json:"origin,omitempty"`
	AddedAt  int64  `json:"added_at"`
}

type rotationState struct {
	Version             int             `json:"version"`
	Enabled             bool            `json:"enabled"`
	Paused              bool            `json:"paused,omitempty"`
	PlaylistID          string          `json:"playlist_id,omitempty"`
	Mode                string          `json:"mode,omitempty"`
	LastBootEntryID     string          `json:"last_boot_entry_id,omitempty"`
	LastBootObjectID    string          `json:"last_boot_object_id,omitempty"`
	LastBootName        string          `json:"last_boot_name,omitempty"`
	LastBootSource      string          `json:"last_boot_source,omitempty"`
	LastBootAt          int64           `json:"last_boot_at,omitempty"`
	LastRotationEntryID string          `json:"last_rotation_entry_id,omitempty"`
	NextEntryID         string          `json:"next_entry_id,omitempty"`
	NextObjectID        string          `json:"next_object_id,omitempty"`
	NextName            string          `json:"next_name,omitempty"`
	NextSource          string          `json:"next_source,omitempty"`
	NextQueueID         string          `json:"next_queue_id,omitempty"`
	PreparedAt          int64           `json:"prepared_at,omitempty"`
	NextReason          string          `json:"next_reason,omitempty"`
	Queue               []bootQueueItem `json:"queue"`
	Override            *bootQueueItem  `json:"override,omitempty"`
	ShuffleRemaining    []string        `json:"shuffle_remaining,omitempty"`
	UpdatedAt           int64           `json:"updated_at,omitempty"`
	LastError           string          `json:"last_error,omitempty"`
}

type rotationResponse struct {
	Status           string          `json:"status"`
	Enabled          bool            `json:"enabled"`
	Paused           bool            `json:"paused"`
	PlaylistID       string          `json:"playlist_id,omitempty"`
	PlaylistName     string          `json:"playlist_name,omitempty"`
	Mode             string          `json:"mode,omitempty"`
	LastBootEntryID  string          `json:"last_boot_entry_id,omitempty"`
	LastBootObjectID string          `json:"last_boot_object_id,omitempty"`
	LastBootName     string          `json:"last_boot_name,omitempty"`
	LastBootSource   string          `json:"last_boot_source,omitempty"`
	LastBootAt       int64           `json:"last_boot_at,omitempty"`
	NextEntryID      string          `json:"next_entry_id,omitempty"`
	NextObjectID     string          `json:"next_object_id,omitempty"`
	NextName         string          `json:"next_name,omitempty"`
	NextSource       string          `json:"next_source,omitempty"`
	PreparedAt       int64           `json:"prepared_at,omitempty"`
	NextReason       string          `json:"next_reason,omitempty"`
	Queue            []bootQueueItem `json:"queue"`
	Override         *bootQueueItem  `json:"override,omitempty"`
	UpdatedAt        int64           `json:"updated_at,omitempty"`
	LastError        string          `json:"last_error,omitempty"`
}

func rotationStatePath() string {
	return filepath.Join(playlistRootDir(), "rotation.json")
}

func normalizeRotationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sequential", "random", "shuffle":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "sequential"
	}
}

func normalizeNextSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "rotation", "queue", "override":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func loadRotationStateLocked() (rotationState, error) {
	if err := ensurePlaylistStorage(); err != nil {
		return rotationState{}, err
	}
	data, err := os.ReadFile(rotationStatePath())
	if os.IsNotExist(err) {
		return rotationState{Version: rotationStateVersion, Mode: "sequential", Queue: []bootQueueItem{}, ShuffleRemaining: []string{}}, nil
	}
	if err != nil {
		return rotationState{}, err
	}
	var state rotationState
	if err := json.Unmarshal(data, &state); err != nil {
		return rotationState{}, errors.New("invalid rotation state")
	}
	rotationVersion := classifyPersistentStateVersion(state.Version, persistentVersionPolicy{
		Domain: "rotation", MinimumVersion: 1, CurrentVersion: rotationStateVersion,
	})
	if rotationVersion != persistentVersionCurrent && rotationVersion != persistentVersionMigration {
		return rotationState{}, errors.New("invalid rotation state")
	}
	if state.Version == 1 && state.LastRotationEntryID == "" {
		state.LastRotationEntryID = state.LastBootEntryID
	}
	state.Version = rotationStateVersion
	state.Mode = normalizeRotationMode(state.Mode)
	state.NextSource = normalizeNextSource(state.NextSource)
	if state.NextSource == "" && state.NextEntryID != "" {
		state.NextSource = "rotation"
	}
	if state.Queue == nil {
		state.Queue = []bootQueueItem{}
	}
	if state.ShuffleRemaining == nil {
		state.ShuffleRemaining = []string{}
	}
	return state, nil
}

func saveRotationStateLocked(state rotationState) error {
	if err := ensurePlaylistStorage(); err != nil {
		return err
	}
	state.Version = rotationStateVersion
	state.Mode = normalizeRotationMode(state.Mode)
	state.NextSource = normalizeNextSource(state.NextSource)
	state.UpdatedAt = time.Now().UnixMilli()
	if state.Queue == nil {
		state.Queue = []bootQueueItem{}
	}
	if state.ShuffleRemaining == nil {
		state.ShuffleRemaining = []string{}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := rotationStatePath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, rotationStatePath())
}

func rotationPlaylistSnapshot(playlistID string) (playlistRecord, bool, error) {
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		return playlistRecord{}, false, err
	}
	playlist, _ := findPlaylistLocked(&state, playlistID)
	if playlist == nil {
		return playlistRecord{}, false, nil
	}
	copyRecord := *playlist
	copyRecord.Items = append([]playlistItemRef(nil), playlist.Items...)
	return copyRecord, true, nil
}

func rotationUsableItems(playlist playlistRecord) []playlistItemRef {
	items := make([]playlistItemRef, 0, len(playlist.Items))
	for _, item := range playlist.Items {
		if !playlistIDPattern.MatchString(item.ID) || !playlistObjectPattern.MatchString(item.ObjectID) {
			continue
		}
		path := playlistObjectZipPath(item.ObjectID)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() <= 0 {
			continue
		}
		items = append(items, item)
	}
	return items
}

func rotationRandomIndex(max int) int {
	if max <= 1 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return int(time.Now().UnixNano() % int64(max))
	}
	return int(value.Int64())
}

func rotationShuffleIDs(ids []string) []string {
	result := append([]string(nil), ids...)
	for i := len(result) - 1; i > 0; i-- {
		j := rotationRandomIndex(i + 1)
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func rotationFindEntry(items []playlistItemRef, entryID string) int {
	for index, item := range items {
		if item.ID == entryID {
			return index
		}
	}
	return -1
}

func chooseRotationItem(state *rotationState, items []playlistItemRef, referenceEntryID string) (playlistItemRef, []string, error) {
	if len(items) == 0 {
		return playlistItemRef{}, nil, errors.New("rotation playlist has no available animations")
	}
	currentShuffle := append([]string(nil), state.ShuffleRemaining...)
	switch normalizeRotationMode(state.Mode) {
	case "random":
		candidates := items
		if len(items) > 1 && referenceEntryID != "" {
			filtered := make([]playlistItemRef, 0, len(items)-1)
			for _, item := range items {
				if item.ID != referenceEntryID {
					filtered = append(filtered, item)
				}
			}
			if len(filtered) > 0 {
				candidates = filtered
			}
		}
		return candidates[rotationRandomIndex(len(candidates))], currentShuffle, nil
	case "shuffle":
		lookup := make(map[string]playlistItemRef, len(items))
		validIDs := make([]string, 0, len(items))
		for _, item := range items {
			lookup[item.ID] = item
			validIDs = append(validIDs, item.ID)
		}
		remaining := make([]string, 0, len(state.ShuffleRemaining))
		seen := make(map[string]struct{})
		for _, id := range state.ShuffleRemaining {
			if _, ok := lookup[id]; !ok {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			remaining = append(remaining, id)
		}
		if len(remaining) == 0 {
			remaining = rotationShuffleIDs(validIDs)
			if len(remaining) > 1 && referenceEntryID != "" && remaining[0] == referenceEntryID {
				remaining[0], remaining[1] = remaining[1], remaining[0]
			}
		}
		selectedID := remaining[0]
		return lookup[selectedID], append([]string(nil), remaining[1:]...), nil
	default:
		index := rotationFindEntry(items, referenceEntryID)
		if index < 0 {
			return items[0], currentShuffle, nil
		}
		return items[(index+1)%len(items)], currentShuffle, nil
	}
}

func clearPreparedNext(state *rotationState) {
	state.NextEntryID = ""
	state.NextObjectID = ""
	state.NextName = ""
	state.NextSource = ""
	state.NextQueueID = ""
	state.PreparedAt = 0
	state.NextReason = ""
}

func validateAutomationObject(objectID string) error {
	if !playlistObjectPattern.MatchString(objectID) {
		return errors.New("invalid automation object")
	}
	objectPath := playlistObjectZipPath(objectID)
	info, err := os.Stat(objectPath)
	if err != nil || info.IsDir() || info.Size() <= 0 {
		return errors.New("selected boot animation is unavailable")
	}
	if err := validateBootAnimation(objectPath); err != nil {
		return errors.New("selected boot animation is invalid")
	}
	return nil
}

func injectAutomationObject(objectID string) error {
	if err := validateAutomationObject(objectID); err != nil {
		return err
	}
	objectPath := playlistObjectZipPath(objectID)
	cmd := exec.Command("/system/bin/sh", filepath.Join(ModDir, "inject.sh"), objectPath)
	cmd.Env = append(os.Environ(), "BAS_SILENT_INJECT=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func prepareQueueItemLocked(state *rotationState, item bootQueueItem, source, reason string) error {
	if !playlistIDPattern.MatchString(item.ID) || !playlistObjectPattern.MatchString(item.ObjectID) {
		return errors.New("queue item is invalid")
	}
	if err := injectAutomationObject(item.ObjectID); err != nil {
		recordBootActivityError(item.Name, source, reason, err)
		return err
	}
	state.NextEntryID = item.ID
	state.NextObjectID = item.ObjectID
	state.NextName = cleanPlaylistName(item.Name, "Boot animation")
	state.NextSource = source
	state.NextQueueID = item.ID
	state.PreparedAt = time.Now().UnixMilli()
	state.NextReason = reason
	state.LastError = ""
	if err := saveRotationStateLocked(*state); err != nil {
		recordBootActivityError(item.Name, source, reason, err)
		return err
	}
	recordBootActivityPrepared(state.NextEntryID, state.NextObjectID, state.NextName, state.NextSource, state.NextReason, state.PreparedAt)
	writeLog("Boot automation prepared next boot: " + state.NextName + " [" + source + ", " + reason + "].")
	return nil
}

func prepareRotationNextLocked(state *rotationState, referenceEntryID string, reason string) error {
	if !state.Enabled {
		return errors.New("rotation is disabled")
	}
	if state.Paused {
		return errors.New("rotation is paused")
	}
	if !playlistIDPattern.MatchString(state.PlaylistID) {
		return errors.New("rotation playlist is invalid")
	}
	playlist, found, err := rotationPlaylistSnapshot(state.PlaylistID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("rotation playlist was not found")
	}
	items := rotationUsableItems(playlist)
	item, nextShuffleRemaining, err := chooseRotationItem(state, items, referenceEntryID)
	if err != nil {
		return err
	}
	if err := injectAutomationObject(item.ObjectID); err != nil {
		recordBootActivityError(item.Name, "rotation", reason, err)
		return err
	}

	candidate := *state
	candidate.ShuffleRemaining = append([]string(nil), nextShuffleRemaining...)
	candidate.NextEntryID = item.ID
	candidate.NextObjectID = item.ObjectID
	candidate.NextName = cleanPlaylistName(item.Name, "Boot animation")
	candidate.NextSource = "rotation"
	candidate.NextQueueID = ""
	candidate.PreparedAt = time.Now().UnixMilli()
	candidate.NextReason = reason
	candidate.LastError = ""
	if err := saveRotationStateLocked(candidate); err != nil {
		recordBootActivityError(item.Name, "rotation", reason, err)
		return err
	}
	*state = candidate
	recordBootActivityPrepared(state.NextEntryID, state.NextObjectID, state.NextName, state.NextSource, state.NextReason, state.PreparedAt)
	writeLog("Rotation prepared next boot: " + state.NextName + " [" + normalizeRotationMode(state.Mode) + ", " + reason + "].")
	return nil
}

type skippedBootQueueItem struct {
	Item bootQueueItem
	Err  error
}

func pruneUnusableQueueHeads(state *rotationState, validateObject func(string) error) []skippedBootQueueItem {
	skipped := []skippedBootQueueItem{}
	for len(state.Queue) > 0 {
		item := state.Queue[0]
		var err error
		if !playlistIDPattern.MatchString(item.ID) {
			err = errors.New("queue item is invalid")
		} else if validateObject == nil {
			err = errors.New("queue validator is unavailable")
		} else {
			err = validateObject(item.ObjectID)
		}
		if err == nil {
			break
		}
		state.Queue = append([]bootQueueItem(nil), state.Queue[1:]...)
		if state.NextSource == "queue" && state.NextQueueID == item.ID {
			clearPreparedNext(state)
		}
		skipped = append(skipped, skippedBootQueueItem{Item: item, Err: err})
	}
	return skipped
}

func automationPriority(state *rotationState) string {
	if state == nil {
		return ""
	}
	if state.Override != nil {
		return "override"
	}
	if len(state.Queue) > 0 {
		return "queue"
	}
	if state.Enabled && !state.Paused {
		return "rotation"
	}
	return ""
}

func prepareAutomationNextLocked(state *rotationState, reason string) error {
	priority := automationPriority(state)
	if priority == "override" {
		return prepareQueueItemLocked(state, *state.Override, "override", reason)
	}
	if priority == "queue" {
		skipped := pruneUnusableQueueHeads(state, validateAutomationObject)
		if len(skipped) > 0 {
			for _, entry := range skipped {
				err := fmt.Errorf("skipped unusable Boot Queue item: %w", entry.Err)
				recordBootActivityError(entry.Item.Name, "queue", reason, err)
				writeLog("Boot Queue skipped unusable item " + entry.Item.ID + ": " + entry.Err.Error())
			}
			if err := saveRotationStateLocked(*state); err != nil {
				return err
			}
		}
		if len(state.Queue) > 0 {
			return prepareQueueItemLocked(state, state.Queue[0], "queue", reason)
		}
	}
	if state.Enabled && !state.Paused {
		return prepareRotationNextLocked(state, state.LastRotationEntryID, reason)
	}
	clearPreparedNext(state)
	state.LastError = ""
	return saveRotationStateLocked(*state)
}

func rotationResponseLocked(state rotationState) rotationResponse {
	response := rotationResponse{
		Status:           "success",
		Enabled:          state.Enabled,
		Paused:           state.Paused,
		PlaylistID:       state.PlaylistID,
		Mode:             normalizeRotationMode(state.Mode),
		LastBootEntryID:  state.LastBootEntryID,
		LastBootObjectID: state.LastBootObjectID,
		LastBootName:     state.LastBootName,
		LastBootSource:   state.LastBootSource,
		LastBootAt:       state.LastBootAt,
		NextEntryID:      state.NextEntryID,
		NextObjectID:     state.NextObjectID,
		NextName:         state.NextName,
		NextSource:       state.NextSource,
		PreparedAt:       state.PreparedAt,
		NextReason:       state.NextReason,
		Queue:            append([]bootQueueItem(nil), state.Queue...),
		UpdatedAt:        state.UpdatedAt,
		LastError:        state.LastError,
	}
	if state.Override != nil {
		copyItem := *state.Override
		response.Override = &copyItem
	}
	if playlistIDPattern.MatchString(state.PlaylistID) {
		if playlist, found, err := rotationPlaylistSnapshot(state.PlaylistID); err == nil && found {
			response.PlaylistName = playlist.Name
		}
	}
	return response
}

func disableRotationForDeletedPlaylist(playlistID string) {
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil || !state.Enabled || state.PlaylistID != playlistID {
		return
	}
	state.Enabled = false
	state.Paused = false
	state.PlaylistID = ""
	state.ShuffleRemaining = []string{}
	state.LastError = "rotation playlist was deleted"
	if state.NextSource == "rotation" {
		clearPreparedNext(&state)
		_ = prepareAutomationNextLocked(&state, "rotation-playlist-deleted")
	} else {
		_ = saveRotationStateLocked(state)
	}
	writeLog("Boot rotation disabled because its playlist was deleted.")
}

func prepareRotationAfterBoot() error {
	rotationMu.Lock()
	defer rotationMu.Unlock()
	state, err := loadRotationStateLocked()
	if err != nil {
		return err
	}
	if state.NextName != "" {
		completedAt := time.Now().UnixMilli()
		appendBootActivity(bootActivityEntry{
			Kind:            "boot",
			Status:          "completed",
			EntryID:         state.NextEntryID,
			ObjectID:        state.NextObjectID,
			Name:            state.NextName,
			Source:          state.NextSource,
			Reason:          state.NextReason,
			PreparedAt:      state.PreparedAt,
			BootCompletedAt: completedAt,
			CreatedAt:       completedAt,
		})
		state.LastBootEntryID = state.NextEntryID
		state.LastBootObjectID = state.NextObjectID
		state.LastBootName = state.NextName
		state.LastBootSource = state.NextSource
		state.LastBootAt = time.Now().UnixMilli()
	}
	switch state.NextSource {
	case "rotation":
		state.LastRotationEntryID = state.NextEntryID
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
	case "override":
		state.Override = nil
	}
	clearPreparedNext(&state)
	if err := prepareAutomationNextLocked(&state, "post-boot"); err != nil {
		state.LastError = err.Error()
		_ = saveRotationStateLocked(state)
		return err
	}
	garbageCollectAutomationObjectsLocked()
	return nil
}
