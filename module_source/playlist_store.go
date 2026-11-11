package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type playlistItemRef struct {
	ID       string `json:"id"`
	ObjectID string `json:"object_id"`
	Name     string `json:"name"`
	AddedAt  int64  `json:"added_at"`
}

type playlistRecord struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	CreatedAt int64             `json:"created_at"`
	UpdatedAt int64             `json:"updated_at"`
	Items     []playlistItemRef `json:"items"`
}

type playlistState struct {
	Version   int              `json:"version"`
	Playlists []playlistRecord `json:"playlists"`
}

func playlistRootDir() string {
	return filepath.Join(ModDir, "playlist")
}

func playlistObjectsDir() string {
	return filepath.Join(playlistRootDir(), "objects")
}

func playlistPreviewsDir() string {
	return filepath.Join(playlistRootDir(), "previews")
}

func playlistStatePath() string {
	return filepath.Join(playlistRootDir(), "playlists.json")
}

func ensurePlaylistStorage() error {
	if err := os.MkdirAll(playlistObjectsDir(), 0755); err != nil {
		return err
	}
	return os.MkdirAll(playlistPreviewsDir(), 0755)
}

func playlistObjectZipPath(objectID string) string {
	return filepath.Join(playlistObjectsDir(), objectID+".zip")
}

func playlistObjectMetadataPath(objectID string) string {
	return filepath.Join(playlistObjectsDir(), objectID+".json")
}

func playlistObjectPreviewPath(objectID string) string {
	return filepath.Join(playlistPreviewsDir(), objectID+".gif")
}

func cleanPlaylistName(value string, fallback string) string {
	name := strings.TrimSpace(value)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = fallback
	}
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	return name
}

func newPlaylistIdentifier(prefix string) (string, error) {
	token, err := randomToken(9)
	if err != nil {
		return "", err
	}
	return prefix + token, nil
}

func loadPlaylistStateLocked() (playlistState, error) {
	if err := ensurePlaylistStorage(); err != nil {
		return playlistState{}, err
	}
	data, err := os.ReadFile(playlistStatePath())
	if os.IsNotExist(err) {
		return playlistState{Version: playlistStateVersion, Playlists: []playlistRecord{}}, nil
	}
	if err != nil {
		return playlistState{}, err
	}
	var state playlistState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != playlistStateVersion {
		return playlistState{}, errors.New("invalid playlist state")
	}
	if state.Playlists == nil {
		state.Playlists = []playlistRecord{}
	}
	return state, nil
}

func savePlaylistStateLocked(state playlistState) error {
	if err := ensurePlaylistStorage(); err != nil {
		return err
	}
	state.Version = playlistStateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := playlistStatePath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, playlistStatePath())
}

func playlistObjectHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writePlaylistObjectMetadata(objectID string, meta historyMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	temp := playlistObjectMetadataPath(objectID) + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(temp, playlistObjectMetadataPath(objectID))
}

func ensurePlaylistObjectMetadata(objectID string) (historyMetadata, error) {
	if !playlistObjectPattern.MatchString(objectID) {
		return historyMetadata{}, errors.New("invalid playlist object")
	}
	if data, err := os.ReadFile(playlistObjectMetadataPath(objectID)); err == nil {
		var meta historyMetadata
		if json.Unmarshal(data, &meta) == nil && meta.SizeBytes >= 0 && meta.Width > 0 && meta.Height > 0 && meta.FPS > 0 {
			return meta, nil
		}
	}
	meta, err := inspectBootAnimation(playlistObjectZipPath(objectID), "")
	if err != nil {
		return historyMetadata{}, err
	}
	_ = writePlaylistObjectMetadata(objectID, meta)
	return meta, nil
}

func ensurePlaylistObject(sourcePath string) (string, historyMetadata, error) {
	if err := validateBootAnimation(sourcePath); err != nil {
		return "", historyMetadata{}, err
	}
	if err := ensurePlaylistStorage(); err != nil {
		return "", historyMetadata{}, err
	}
	objectID, err := playlistObjectHash(sourcePath)
	if err != nil {
		return "", historyMetadata{}, err
	}
	destination := playlistObjectZipPath(objectID)
	if info, statErr := os.Stat(destination); statErr != nil || info.IsDir() {
		if err := linkOrCopyFile(sourcePath, destination, 0644); err != nil {
			return "", historyMetadata{}, err
		}
	}
	meta, err := ensurePlaylistObjectMetadata(objectID)
	if err != nil {
		_ = os.Remove(destination)
		return "", historyMetadata{}, err
	}
	return objectID, meta, nil
}

func playlistObjectReferencedLocked(state playlistState, objectID string) bool {
	for _, playlist := range state.Playlists {
		for _, item := range playlist.Items {
			if item.ObjectID == objectID {
				return true
			}
		}
	}
	return false
}

func automationObjectReferencesFromDisk() map[string]struct{} {
	references := make(map[string]struct{})
	data, err := os.ReadFile(rotationStatePath())
	if err != nil {
		return references
	}
	var state rotationState
	if json.Unmarshal(data, &state) != nil {
		return references
	}
	for _, item := range state.Queue {
		if playlistObjectPattern.MatchString(item.ObjectID) {
			references[item.ObjectID] = struct{}{}
		}
	}
	if state.Override != nil && playlistObjectPattern.MatchString(state.Override.ObjectID) {
		references[state.Override.ObjectID] = struct{}{}
	}
	if playlistObjectPattern.MatchString(state.NextObjectID) && (state.NextSource == "queue" || state.NextSource == "override") {
		references[state.NextObjectID] = struct{}{}
	}
	return references
}

func garbageCollectPlaylistObjectsLocked(state playlistState) {
	entries, err := os.ReadDir(playlistObjectsDir())
	if err != nil {
		return
	}
	automationRefs := automationObjectReferencesFromDisk()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		objectID := strings.TrimSuffix(entry.Name(), ".zip")
		if !playlistObjectPattern.MatchString(objectID) || playlistObjectReferencedLocked(state, objectID) {
			continue
		}
		if _, usedByAutomation := automationRefs[objectID]; usedByAutomation {
			continue
		}
		_ = os.Remove(playlistObjectZipPath(objectID))
		_ = os.Remove(playlistObjectMetadataPath(objectID))
		_ = os.Remove(playlistObjectPreviewPath(objectID))
	}
}

func playlistResponseLocked(record playlistRecord) playlistResponse {
	response := playlistResponse{ID: record.ID, Name: record.Name, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, Items: []playlistItemResponse{}}
	for _, ref := range record.Items {
		meta, err := ensurePlaylistObjectMetadata(ref.ObjectID)
		if err != nil {
			continue
		}
		preview := false
		if info, statErr := os.Stat(playlistObjectPreviewPath(ref.ObjectID)); statErr == nil && !info.IsDir() && info.Size() > 0 {
			preview = true
		}
		response.Items = append(response.Items, playlistItemResponse{ID: ref.ID, ObjectID: ref.ObjectID, Name: ref.Name, AddedAt: ref.AddedAt, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, FPS: meta.FPS, FrameCount: meta.FrameCount, Parts: meta.Parts, FrameFormat: meta.FrameFormat, HasAudio: meta.HasAudio, HasPreview: preview})
	}
	return response
}

func findPlaylistLocked(state *playlistState, id string) (*playlistRecord, int) {
	for index := range state.Playlists {
		if state.Playlists[index].ID == id {
			return &state.Playlists[index], index
		}
	}
	return nil, -1
}

func addPlaylistItemLocked(state *playlistState, playlistID, objectID, name string) (playlistItemRef, error) {
	playlist, _ := findPlaylistLocked(state, playlistID)
	if playlist == nil {
		return playlistItemRef{}, errors.New("playlist not found")
	}
	entryID, err := newPlaylistIdentifier("it_")
	if err != nil {
		return playlistItemRef{}, err
	}
	now := time.Now().UnixMilli()
	displayName := strings.TrimSpace(name)
	if strings.EqualFold(filepath.Ext(displayName), ".zip") {
		displayName = strings.TrimSuffix(displayName, filepath.Ext(displayName))
	}
	item := playlistItemRef{ID: entryID, ObjectID: objectID, Name: cleanPlaylistName(displayName, "Boot animation"), AddedAt: now}
	playlist.Items = append(playlist.Items, item)
	playlist.UpdatedAt = now
	return item, nil
}

func decodePlaylistMutation(w http.ResponseWriter, r *http.Request) (playlistMutationRequest, bool) {
	var request playlistMutationRequest
	if err := decodeJSONRequest(r, 64<<10, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid request"})
		return playlistMutationRequest{}, false
	}
	return request, true
}
