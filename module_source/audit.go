package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type securityAuditActor struct {
	Type       string `json:"type"`
	ClientID   string `json:"client_id,omitempty"`
	Label      string `json:"label,omitempty"`
	Permission string `json:"permission,omitempty"`
	ClientIP   string `json:"client_ip,omitempty"`
}

type securityAuditEvent struct {
	ID        string             `json:"id"`
	Timestamp int64              `json:"timestamp"`
	Category  string             `json:"category"`
	Action    string             `json:"action"`
	Actor     securityAuditActor `json:"actor"`
	Target    string             `json:"target,omitempty"`
	Details   map[string]string  `json:"details,omitempty"`
}

type securityAuditState struct {
	Version int                  `json:"version"`
	Events  []securityAuditEvent `json:"events"`
}

type auditStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditStatusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *auditStatusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func securityAuditPath() string {
	return filepath.Join(ModDir, "trust", "security_audit.json")
}

func loadSecurityAuditLocked() (securityAuditState, error) {
	data, err := os.ReadFile(securityAuditPath())
	if os.IsNotExist(err) {
		return securityAuditState{Version: securityAuditVersion, Events: []securityAuditEvent{}}, nil
	}
	if err != nil {
		return securityAuditState{}, err
	}
	var state securityAuditState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != securityAuditVersion {
		return securityAuditState{}, errors.New("invalid security audit state")
	}
	if state.Events == nil {
		state.Events = []securityAuditEvent{}
	}
	if len(state.Events) > securityAuditLimit {
		state.Events = append([]securityAuditEvent(nil), state.Events[len(state.Events)-securityAuditLimit:]...)
	}
	return state, nil
}

func saveSecurityAuditLocked(state securityAuditState) error {
	if err := os.MkdirAll(filepath.Dir(securityAuditPath()), 0700); err != nil {
		return err
	}
	state.Version = securityAuditVersion
	if len(state.Events) > securityAuditLimit {
		state.Events = append([]securityAuditEvent(nil), state.Events[len(state.Events)-securityAuditLimit:]...)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := securityAuditPath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, securityAuditPath()); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return os.Chmod(securityAuditPath(), 0600)
}

func securityAuditActorForRequest(r *http.Request) securityAuditActor {
	ip := getIP(r)
	if isWebUIAuthorized(r) {
		return securityAuditActor{Type: "webui", Label: "Local Module WebUI", Permission: "admin", ClientIP: ip}
	}
	if session, _, ok := trustedSessionForRequest(r); ok {
		return securityAuditActor{Type: "trusted", ClientID: session.ClientID, Label: session.ClientLabel, Permission: session.Permission, ClientIP: session.ClientIP}
	}
	stateMu.RLock()
	legacy := pairedIP != "" && pairedIP == ip && secureEqual(pairedToken, r.Header.Get("X-Boot-Creator-Token"))
	stateMu.RUnlock()
	if legacy {
		return securityAuditActor{Type: "legacy", Label: "Legacy BAS website", Permission: "admin", ClientIP: ip}
	}
	return securityAuditActor{Type: "unknown", ClientIP: ip}
}

func appendSecurityAudit(event securityAuditEvent) {
	securityAuditMu.Lock()
	defer securityAuditMu.Unlock()
	state, err := loadSecurityAuditLocked()
	if err != nil {
		writeLog("Warning: Could not load Security Audit: " + err.Error())
		state = securityAuditState{Version: securityAuditVersion, Events: []securityAuditEvent{}}
	}
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	if event.ID == "" {
		suffix, err := randomToken(6)
		if err != nil {
			suffix = strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		event.ID = "audit_" + strconv.FormatInt(event.Timestamp, 10) + "_" + suffix
	}
	state.Events = append(state.Events, event)
	if len(state.Events) > securityAuditLimit {
		state.Events = append([]securityAuditEvent(nil), state.Events[len(state.Events)-securityAuditLimit:]...)
	}
	if err := saveSecurityAuditLocked(state); err != nil {
		writeLog("Warning: Could not save Security Audit: " + err.Error())
		return
	}
	emitLiveEvent("audit", "audit.changed")
}

func recordSecurityAudit(r *http.Request, category, action, target string, details map[string]string) {
	appendSecurityAudit(securityAuditEvent{Category: category, Action: action, Actor: securityAuditActorForRequest(r), Target: target, Details: details})
}

func recordCompanionSecurityAudit(action, clientID, label, clientIP string, details map[string]string) {
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: action, Actor: securityAuditActor{Type: "companion", Label: "Companion approval", Permission: "admin"}, Target: clientID, Details: mergeAuditDetails(details, map[string]string{"client_label": cleanClientLabel(label), "client_ip": clientIP})})
}

func mergeAuditDetails(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	for key, value := range extra {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func auditedHandler(category, action string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := securityAuditActorForRequest(r)
		recorder := &auditStatusWriter{ResponseWriter: w}
		handler(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 200 && status < 400 {
			appendSecurityAudit(securityAuditEvent{Category: category, Action: action, Actor: actor})
			emitLiveAuditEvent(category, action)
		}
	}
}

func securityAuditListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	securityAuditMu.Lock()
	state, err := loadSecurityAuditLocked()
	securityAuditMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not load Security Audit"})
		return
	}
	events := append([]securityAuditEvent(nil), state.Events...)
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": securityAuditVersion, "limit": securityAuditLimit, "events": events})
}

func securityAuditClearHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	securityAuditMu.Lock()
	err := saveSecurityAuditLocked(securityAuditState{Version: securityAuditVersion, Events: []securityAuditEvent{}})
	securityAuditMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not clear Security Audit"})
		return
	}
	appendSecurityAudit(securityAuditEvent{Category: "security", Action: "audit.cleared", Actor: actor})
	writeJSON(w, http.StatusOK, statusResponse{Status: "cleared"})
	showToastEvent("toast_audit_cleared", "🛡️ Boot Creator: Security Audit was cleared.")
}

func securityAuditExportHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	securityAuditMu.Lock()
	state, err := loadSecurityAuditLocked()
	securityAuditMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not export Security Audit"})
		return
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not export Security Audit"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="boot-animation-studio-security-audit.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(data, '\n'))
}
