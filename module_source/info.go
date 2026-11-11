package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type statusResponse struct {
	Status     string `json:"status"`
	Model      string `json:"model,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	HasCustom  bool   `json:"has_custom,omitempty"`
	BridgeID   string `json:"bridge_id,omitempty"`
	Token      string `json:"token,omitempty"`
	Permission string `json:"permission,omitempty"`
	Message    string `json:"message,omitempty"`
}

type infoResponse struct {
	API                      int      `json:"api_version"`
	BridgeID                 string   `json:"bridge_id,omitempty"`
	Model                    string   `json:"model,omitempty"`
	ModuleVersion            string   `json:"module_version"`
	ModuleVersionCode        int      `json:"module_version_code"`
	CompanionVersionCode     int      `json:"companion_version_code"`
	Features                 []string `json:"features"`
	MaxDirectUploadBytes     int64    `json:"max_direct_upload_bytes,omitempty"`
	DirectUploadWarningBytes int64    `json:"direct_upload_warning_bytes,omitempty"`
	HistoryLimit             int      `json:"history_limit,omitempty"`
}

func loadOrCreateBridgeID() string {
	path := filepath.Join(ModDir, bridgeIdentityFile)
	if data, err := os.ReadFile(path); err == nil {
		candidate := strings.TrimSpace(string(data))
		if bridgeIDPattern.MatchString(candidate) {
			return candidate
		}
		writeLog("Warning: Existing bridge identity is invalid; generating a new identity.")
	}

	random, err := randomToken(18)
	if err != nil {
		writeLog("Warning: Could not generate persistent bridge identity: " + err.Error())
		return ""
	}
	candidate := "bc_" + random
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(candidate+"\n"), 0600); err != nil {
		writeLog("Warning: Could not persist bridge identity: " + err.Error())
		return candidate
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		writeLog("Warning: Could not activate persistent bridge identity: " + err.Error())
		return candidate
	}
	_ = os.Chmod(path, 0600)
	return candidate
}

func infoHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	version, versionCode := readModuleMetadata()
	writeJSON(w, http.StatusOK, infoResponse{
		API:                      apiVersion,
		BridgeID:                 bridgeID,
		Model:                    deviceModel,
		ModuleVersion:            version,
		ModuleVersionCode:        versionCode,
		CompanionVersionCode:     companionVersionCode,
		Features:                 moduleFeatures(),
		MaxDirectUploadBytes:     maxBootAnimationBytes,
		DirectUploadWarningBytes: directUploadWarningBytes,
		HistoryLimit:             historyLimit,
	})
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	if !isAuthorized(r) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "auth_required"})
		return
	}

	permission, _ := requestAuthorizationPermission(r)
	writeJSON(w, http.StatusOK, statusResponse{
		Status:     "ok",
		Model:      deviceModel,
		Resolution: deviceResolution,
		HasCustom:  checkHasCustomAnim(),
		BridgeID:   bridgeID,
		Permission: permission,
	})
}
