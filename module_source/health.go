package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type moduleHealthCheck struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
	Count  int    `json:"count,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

type moduleHealthAction struct {
	ID         string `json:"id"`
	Count      int    `json:"count,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	Permission string `json:"required_permission"`
}

type moduleHealthActionRequest struct {
	Action string `json:"action"`
}

type moduleHealthActionResponse struct {
	Status       string               `json:"status"`
	Action       string               `json:"action"`
	RemovedCount int                  `json:"removed_count"`
	RemovedBytes int64                `json:"removed_bytes"`
	Health       moduleHealthResponse `json:"health"`
}

type moduleStorageBucket struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

type moduleHealthStorage struct {
	FilesystemAvailable  bool                  `json:"filesystem_available"`
	FilesystemTotalBytes int64                 `json:"filesystem_total_bytes"`
	FilesystemFreeBytes  int64                 `json:"filesystem_free_bytes"`
	ModuleBytes          int64                 `json:"module_bytes"`
	ModuleFiles          int                   `json:"module_files"`
	OrphanObjectBytes    int64                 `json:"orphan_object_bytes,omitempty"`
	OrphanObjectCount    int                   `json:"orphan_object_count,omitempty"`
	Buckets              []moduleStorageBucket `json:"buckets"`
}

type moduleHealthResponse struct {
	Status        string               `json:"status"`
	SchemaVersion int                  `json:"schema_version"`
	CollectedAt   int64                `json:"collected_at"`
	Overall       string               `json:"overall"`
	OKCount       int                  `json:"ok_count"`
	WarningCount  int                  `json:"warning_count"`
	ErrorCount    int                  `json:"error_count"`
	Checks        []moduleHealthCheck  `json:"checks"`
	Storage       moduleHealthStorage  `json:"storage"`
	Actions       []moduleHealthAction `json:"actions,omitempty"`
}

func collectPathUsage(root string) (int64, int) {
	var total int64
	files := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
			files++
		}
		return nil
	})
	return total, files
}

func healthFilesystemUsage() (int64, int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(ModDir, &stat); err != nil {
		return 0, 0, err
	}
	blockSize := int64(stat.Bsize)
	return int64(stat.Blocks) * blockSize, int64(stat.Bavail) * blockSize, nil
}

func healthFileState(path string, expectedVersion int, target interface{}) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	_ = expectedVersion
	return nil
}

func collectModuleHealth() moduleHealthResponse {
	checks := make([]moduleHealthCheck, 0, 10)
	add := func(check moduleHealthCheck) { checks = append(checks, check) }

	bootProbe := collectDeviceBootProbe()
	paths, pathsErr := readSavedPaths()
	switch {
	case pathsErr != nil:
		add(moduleHealthCheck{ID: "boot_paths", State: "error", Code: "unreadable", Detail: pathsErr.Error()})
	case len(paths) == 0:
		add(moduleHealthCheck{ID: "boot_paths", State: "error", Code: "empty"})
	default:
		add(moduleHealthCheck{ID: "boot_paths", State: "ok", Code: "detected", Detail: paths[0], Count: len(paths)})
	}

	if bootProbe.CustomApplied {
		if bootProbe.CurrentArchive.Status == "ok" {
			add(moduleHealthCheck{ID: "current_animation", State: "ok", Code: "custom_valid", Detail: bootProbe.CurrentArchive.Format})
		} else {
			add(moduleHealthCheck{ID: "current_animation", State: "error", Code: "custom_invalid", Detail: bootProbe.CurrentArchive.Status})
		}
	} else {
		add(moduleHealthCheck{ID: "current_animation", State: "ok", Code: "stock_active", Detail: bootProbe.CurrentArchive.Format})
	}

	if bootProbe.CustomApplied {
		if bootProbe.StockArchive.Status == "ok" {
			add(moduleHealthCheck{ID: "stock_backup", State: "ok", Code: "available", Detail: bootProbe.StockArchive.Format})
		} else {
			add(moduleHealthCheck{ID: "stock_backup", State: "warn", Code: "missing_or_invalid", Detail: bootProbe.StockArchive.Status})
		}
	} else {
		add(moduleHealthCheck{ID: "stock_backup", State: "ok", Code: "not_required"})
	}

	historyInvalid := 0
	historyCount := 0
	if entries, err := os.ReadDir(filepath.Join(ModDir, "history")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".zip")
			if !validHistoryID(id) {
				historyInvalid++
				continue
			}
			historyCount++
			if data, err := os.ReadFile(historyMetadataPath(id)); err == nil {
				var meta historyMetadata
				if json.Unmarshal(data, &meta) != nil || meta.Version != historyMetadataVersion {
					historyInvalid++
				}
			} else if !os.IsNotExist(err) {
				historyInvalid++
			}
		}
	} else if !os.IsNotExist(err) {
		historyInvalid++
	}
	if historyInvalid > 0 {
		add(moduleHealthCheck{ID: "history_integrity", State: "warn", Code: "invalid_entries", Count: historyInvalid})
	} else {
		add(moduleHealthCheck{ID: "history_integrity", State: "ok", Code: "clean", Count: historyCount})
	}

	playlistStateValue := playlistState{Version: playlistStateVersion, Playlists: []playlistRecord{}}
	playlistErr := healthFileState(playlistStatePath(), playlistStateVersion, &playlistStateValue)
	missingObjects := 0
	referenced := make(map[string]struct{})
	if playlistErr == nil {
		if playlistStateValue.Version != 0 && playlistStateValue.Version != playlistStateVersion {
			playlistErr = errors.New("unsupported playlist state version")
		} else {
			for _, playlist := range playlistStateValue.Playlists {
				for _, item := range playlist.Items {
					if !playlistObjectPattern.MatchString(item.ObjectID) {
						missingObjects++
						continue
					}
					referenced[item.ObjectID] = struct{}{}
					info, err := os.Stat(playlistObjectZipPath(item.ObjectID))
					if err != nil || info.IsDir() || info.Size() <= 0 {
						missingObjects++
					}
				}
			}
		}
	}
	if playlistErr != nil {
		add(moduleHealthCheck{ID: "playlist_integrity", State: "error", Code: "state_invalid", Detail: playlistErr.Error()})
	} else if missingObjects > 0 {
		add(moduleHealthCheck{ID: "playlist_integrity", State: "warn", Code: "missing_objects", Count: missingObjects})
	} else {
		add(moduleHealthCheck{ID: "playlist_integrity", State: "ok", Code: "clean", Count: len(referenced)})
	}

	rotationValue := rotationState{Version: rotationStateVersion, Queue: []bootQueueItem{}}
	rotationErr := healthFileState(rotationStatePath(), rotationStateVersion, &rotationValue)
	automationMissing := 0
	if rotationErr == nil && rotationValue.Version != 0 {
		if rotationValue.Version < 1 || rotationValue.Version > rotationStateVersion {
			rotationErr = errors.New("unsupported rotation state version")
		} else {
			if rotationValue.PlaylistID != "" {
				found := false
				for _, playlist := range playlistStateValue.Playlists {
					if playlist.ID == rotationValue.PlaylistID {
						found = true
						break
					}
				}
				if !found {
					automationMissing++
				}
			}
			for _, item := range rotationValue.Queue {
				if item.ObjectID != "" {
					referenced[item.ObjectID] = struct{}{}
					if info, err := os.Stat(playlistObjectZipPath(item.ObjectID)); err != nil || info.IsDir() || info.Size() <= 0 {
						automationMissing++
					}
				}
			}
			if rotationValue.Override != nil && rotationValue.Override.ObjectID != "" {
				referenced[rotationValue.Override.ObjectID] = struct{}{}
				if info, err := os.Stat(playlistObjectZipPath(rotationValue.Override.ObjectID)); err != nil || info.IsDir() || info.Size() <= 0 {
					automationMissing++
				}
			}
			if rotationValue.NextObjectID != "" && (rotationValue.NextSource == "queue" || rotationValue.NextSource == "override") {
				referenced[rotationValue.NextObjectID] = struct{}{}
			}
		}
	}
	if rotationErr != nil {
		add(moduleHealthCheck{ID: "automation_integrity", State: "error", Code: "state_invalid", Detail: rotationErr.Error()})
	} else if automationMissing > 0 {
		add(moduleHealthCheck{ID: "automation_integrity", State: "warn", Code: "missing_references", Count: automationMissing})
	} else {
		add(moduleHealthCheck{ID: "automation_integrity", State: "ok", Code: "clean", Count: len(rotationValue.Queue)})
	}

	stagingInvalid := false
	var stagingInvalidBytes int64
	if info, err := os.Stat(stagedAnimationPath()); err == nil && !info.IsDir() {
		probe := inspectBootArchiveProbe(stagedAnimationPath())
		if probe.Status == "ok" {
			add(moduleHealthCheck{ID: "staging_integrity", State: "ok", Code: "staged_valid", Bytes: info.Size()})
		} else {
			stagingInvalid = true
			stagingInvalidBytes = info.Size()
			add(moduleHealthCheck{ID: "staging_integrity", State: "warn", Code: "staged_invalid", Detail: probe.Status, Bytes: info.Size()})
		}
	} else {
		add(moduleHealthCheck{ID: "staging_integrity", State: "ok", Code: "empty"})
	}

	trustMu.Lock()
	_, trustErr := loadTrustStateLocked()
	trustMu.Unlock()
	securityAuditMu.Lock()
	_, auditErr := loadSecurityAuditLocked()
	securityAuditMu.Unlock()
	if trustErr != nil || auditErr != nil {
		detail := ""
		if trustErr != nil {
			detail = trustErr.Error()
		} else {
			detail = auditErr.Error()
		}
		add(moduleHealthCheck{ID: "security_state", State: "error", Code: "state_invalid", Detail: detail})
	} else {
		add(moduleHealthCheck{ID: "security_state", State: "ok", Code: "clean"})
	}

	staleTemps := 0
	var staleTempBytes int64
	_ = filepath.Walk(ModDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".tmp") && time.Since(info.ModTime()) > 5*time.Minute {
			staleTemps++
			staleTempBytes += info.Size()
		}
		return nil
	})
	if staleTemps > 0 {
		add(moduleHealthCheck{ID: "temporary_files", State: "warn", Code: "stale", Count: staleTemps})
	} else {
		add(moduleHealthCheck{ID: "temporary_files", State: "ok", Code: "clean"})
	}

	orphanCount := 0
	var orphanBytes int64
	if entries, err := os.ReadDir(playlistObjectsDir()); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
				continue
			}
			objectID := strings.TrimSuffix(entry.Name(), ".zip")
			if !playlistObjectPattern.MatchString(objectID) {
				continue
			}
			if _, used := referenced[objectID]; used {
				continue
			}
			if info, err := entry.Info(); err == nil {
				orphanBytes += info.Size()
			}
			orphanCount++
		}
	}
	if orphanCount > 0 {
		add(moduleHealthCheck{ID: "orphan_objects", State: "warn", Code: "found", Count: orphanCount, Bytes: orphanBytes})
	} else {
		add(moduleHealthCheck{ID: "orphan_objects", State: "ok", Code: "none"})
	}

	totalFS, freeFS, filesystemErr := healthFilesystemUsage()
	storageState := "ok"
	storageCode := "space_ok"
	storageDetail := ""
	if filesystemErr != nil {
		storageState, storageCode, storageDetail = "warn", "space_unknown", filesystemErr.Error()
	} else if freeFS < 64<<20 {
		storageState, storageCode = "error", "space_critical"
	} else if freeFS < 256<<20 {
		storageState, storageCode = "warn", "space_low"
	}
	add(moduleHealthCheck{ID: "free_space", State: storageState, Code: storageCode, Detail: storageDetail, Bytes: freeFS})

	bucketPaths := []struct{ id, path string }{
		{"history", filepath.Join(ModDir, "history")},
		{"playlist", playlistRootDir()},
		{"backup", filepath.Join(ModDir, "backup")},
		{"staging", filepath.Join(ModDir, "staging")},
		{"trust", filepath.Join(ModDir, "trust")},
	}
	buckets := make([]moduleStorageBucket, 0, len(bucketPaths)+2)
	var categorized int64
	for _, item := range bucketPaths {
		bytes, files := collectPathUsage(item.path)
		buckets = append(buckets, moduleStorageBucket{ID: item.id, Bytes: bytes, Files: files})
		categorized += bytes
	}
	logBytes := int64(0)
	logFiles := 0
	for _, path := range []string{filepath.Join(ModDir, "boot_creator.log"), filepath.Join(ModDir, "boot_creator.previous.log")} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			logBytes += info.Size()
			logFiles++
		}
	}
	buckets = append(buckets, moduleStorageBucket{ID: "logs", Bytes: logBytes, Files: logFiles})
	categorized += logBytes
	moduleBytes, moduleFiles := collectPathUsage(ModDir)
	runtimeBytes := moduleBytes - categorized
	if runtimeBytes < 0 {
		runtimeBytes = 0
	}
	buckets = append(buckets, moduleStorageBucket{ID: "runtime", Bytes: runtimeBytes, Files: 0})

	okCount, warningCount, errorCount := 0, 0, 0
	for _, check := range checks {
		switch check.State {
		case "error":
			errorCount++
		case "warn":
			warningCount++
		default:
			okCount++
		}
	}
	overall := "healthy"
	if errorCount > 0 {
		overall = "degraded"
	} else if warningCount > 0 {
		overall = "attention"
	}
	actions := make([]moduleHealthAction, 0, 3)
	if staleTemps > 0 {
		actions = append(actions, moduleHealthAction{ID: "cleanup_stale_temps", Count: staleTemps, Bytes: staleTempBytes, Permission: "manage"})
	}
	if orphanCount > 0 {
		actions = append(actions, moduleHealthAction{ID: "cleanup_orphan_objects", Count: orphanCount, Bytes: orphanBytes, Permission: "manage"})
	}
	if stagingInvalid {
		actions = append(actions, moduleHealthAction{ID: "clear_invalid_staging", Count: 1, Bytes: stagingInvalidBytes, Permission: "manage"})
	}
	return moduleHealthResponse{
		Status: "ok", SchemaVersion: 2, CollectedAt: time.Now().UnixMilli(), Overall: overall,
		OKCount: okCount, WarningCount: warningCount, ErrorCount: errorCount, Checks: checks,
		Storage: moduleHealthStorage{FilesystemAvailable: filesystemErr == nil, FilesystemTotalBytes: totalFS, FilesystemFreeBytes: freeFS, ModuleBytes: moduleBytes, ModuleFiles: moduleFiles, OrphanObjectBytes: orphanBytes, OrphanObjectCount: orphanCount, Buckets: buckets},
		Actions: actions,
	}
}

func cleanupHealthStaleTemps() (int, int64, error) {
	removed := 0
	var removedBytes int64
	err := filepath.Walk(ModDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".tmp") || time.Since(info.ModTime()) <= 5*time.Minute {
			return nil
		}
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
		removed++
		removedBytes += info.Size()
		return nil
	})
	return removed, removedBytes, err
}

func cleanupHealthOrphanObjects() (int, int64, error) {
	rotationMu.Lock()
	defer rotationMu.Unlock()
	playlistMu.Lock()
	defer playlistMu.Unlock()
	state, err := loadPlaylistStateLocked()
	if err != nil {
		return 0, 0, err
	}
	automationRefs := automationObjectReferencesFromDisk()
	entries, err := os.ReadDir(playlistObjectsDir())
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	removed := 0
	var removedBytes int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		objectID := strings.TrimSuffix(entry.Name(), ".zip")
		if !playlistObjectPattern.MatchString(objectID) || playlistObjectReferencedLocked(state, objectID) {
			continue
		}
		if _, used := automationRefs[objectID]; used {
			continue
		}
		paths := []string{playlistObjectZipPath(objectID), playlistObjectMetadataPath(objectID), playlistObjectPreviewPath(objectID)}
		for _, path := range paths {
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				removedBytes += info.Size()
			}
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return removed, removedBytes, removeErr
			}
		}
		removed++
	}
	return removed, removedBytes, nil
}

func cleanupHealthInvalidStaging() (int, int64, error) {
	path := stagedAnimationPath()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if info.IsDir() {
		return 0, 0, errors.New("invalid staging path")
	}
	if inspectBootArchiveProbe(path).Status == "ok" {
		return 0, 0, errors.New("staged animation is valid")
	}
	bytes := info.Size()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return 0, 0, err
	}
	cleanupPreviewShadows()
	return 1, bytes, nil
}

func moduleHealthActionHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	var input moduleHealthActionRequest
	if err := decodeJSONRequest(r, 32<<10, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid health maintenance request"})
		return
	}
	action := strings.TrimSpace(input.Action)
	removedCount := 0
	var removedBytes int64
	var err error
	switch action {
	case "cleanup_stale_temps":
		removedCount, removedBytes, err = cleanupHealthStaleTemps()
	case "cleanup_orphan_objects":
		removedCount, removedBytes, err = cleanupHealthOrphanObjects()
	case "clear_invalid_staging":
		removedCount, removedBytes, err = cleanupHealthInvalidStaging()
	default:
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Unsupported health maintenance action"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: err.Error()})
		return
	}
	recordSecurityAudit(r, "maintenance", "health."+action, action, map[string]string{"removed_count": strconv.Itoa(removedCount), "removed_bytes": strconv.FormatInt(removedBytes, 10)})
	writeLog("Health maintenance completed: " + action + " removed=" + strconv.Itoa(removedCount) + " bytes=" + strconv.FormatInt(removedBytes, 10))
	showToastEvent("toast_health_maintenance", "🩺 Boot Creator: Module health maintenance completed.")
	writeJSON(w, http.StatusOK, moduleHealthActionResponse{Status: "ok", Action: action, RemovedCount: removedCount, RemovedBytes: removedBytes, Health: collectModuleHealth()})
}

func moduleHealthHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, collectModuleHealth())
}
