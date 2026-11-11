package main

import (
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type trustSessionInput struct {
	SessionID string `json:"session_id"`
}

type trustClientInput struct {
	ClientID string `json:"client_id"`
}

type trustPermissionInput struct {
	ClientID   string `json:"client_id"`
	Permission string `json:"permission"`
}

type trustVerifyInput struct {
	ClientID    string `json:"client_id"`
	ChallengeID string `json:"challenge_id"`
	Signature   string `json:"signature"`
}

func trustedAuthChallengeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	var input trustClientInput
	if err := decodeJSONRequest(r, 8<<10, &input); err != nil || !clientIDPattern.MatchString(strings.TrimSpace(input.ClientID)) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client identity"})
		return
	}
	if !enforceAuthRateLimit(w, r, "trusted_challenge") {
		return
	}
	challengeID, message, err := createTrustChallenge(strings.TrimSpace(input.ClientID), getIP(r))
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "untrusted"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create trusted-client challenge"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "challenge", "challenge_id": challengeID, "message": message})
}

func trustedAuthVerifyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	var input trustVerifyInput
	if err := decodeJSONRequest(r, 16<<10, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client proof"})
		return
	}
	token, err := verifyTrustChallenge(input, getIP(r))
	if err != nil {
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "untrusted", Message: "Trusted-client verification failed"})
		return
	}
	permission := "admin"
	if session, _, ok := trustedSessionForToken(strings.TrimSpace(input.ClientID), getIP(r), token); ok {
		permission = session.Permission
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "token": token, "client_id": strings.TrimSpace(input.ClientID), "permission": permission,
		"model": deviceModel, "resolution": deviceResolution, "has_custom": checkHasCustomAnim(), "bridge_id": bridgeID,
	})
	client, _, _ := trustedClientByID(strings.TrimSpace(input.ClientID))
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "auth.reconnected", Actor: securityAuditActor{Type: "trusted", ClientID: strings.TrimSpace(input.ClientID), Label: client.Label, Permission: permission, ClientIP: getIP(r)}})
	emitLiveEvent("sessions", "sessions.changed")
	writeLog("Trusted BAS client reconnected from IP: " + getIP(r))
}

func trustClientsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	currentID := currentTrustedClientID(r)
	trustMu.Lock()
	state, err := loadTrustStateLocked()
	trustMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read trusted clients"})
		return
	}
	counts := make(map[string]int)
	trustedSessionMu.RLock()
	for _, session := range trustedSessions {
		counts[session.ClientID]++
	}
	trustedSessionMu.RUnlock()
	clients := make([]trustedClientView, 0, len(state.Clients))
	for _, client := range state.Clients {
		clients = append(clients, trustedClientView{
			ID: client.ID, Label: client.Label, Permission: client.Permission, CreatedAt: client.CreatedAt, LastApprovedAt: client.LastApprovedAt,
			LastSeenAt: client.LastSeenAt, LastIP: client.LastIP, ActiveSessions: counts[client.ID], Current: client.ID == currentID,
		})
	}
	sort.SliceStable(clients, func(i, j int) bool { return clients[i].LastSeenAt > clients[j].LastSeenAt })
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": trustStateVersion, "clients": clients})
}

func trustSessionsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	currentSessionID := ""
	if session, _, ok := trustedSessionForRequest(r); ok {
		currentSessionID = session.ID
	}
	sessions := make([]trustSessionView, 0, 8)
	trustedSessionMu.RLock()
	for _, session := range trustedSessions {
		sessions = append(sessions, trustSessionView{
			ID: session.ID, ClientID: session.ClientID, Label: session.ClientLabel, ClientIP: session.ClientIP, Permission: session.Permission,
			Persistent: session.Persistent, CreatedAt: session.CreatedAt.UnixMilli(), LastSeenAt: session.LastSeen.UnixMilli(), Current: session.ID == currentSessionID,
		})
	}
	trustedSessionMu.RUnlock()
	stateMu.RLock()
	legacyIP := pairedIP
	legacyToken := pairedToken
	legacyCreated := pairedCreatedAt
	legacySeen := pairedLastSeen
	stateMu.RUnlock()
	if legacyIP != "" && legacyToken != "" {
		currentLegacy := getIP(r) == legacyIP && secureEqual(r.Header.Get("X-Boot-Creator-Token"), legacyToken)
		sessions = append(sessions, trustSessionView{ID: "legacy", Label: "Legacy BAS website", ClientIP: legacyIP, Permission: "admin", CreatedAt: legacyCreated.UnixMilli(), LastSeenAt: legacySeen.UnixMilli(), Current: currentLegacy, Legacy: true})
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].LastSeenAt > sessions[j].LastSeenAt })
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "sessions": sessions})
}

func trustSessionDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	var input trustSessionInput
	if err := decodeJSONRequest(r, 8<<10, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid session request"})
		return
	}
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "legacy" {
		stateMu.Lock()
		existed := pairedIP != "" && pairedToken != ""
		pairedIP = ""
		pairedToken = ""
		pairedCreatedAt = time.Time{}
		pairedLastSeen = time.Time{}
		stateMu.Unlock()
		if !existed {
			writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Session not found"})
			return
		}
		writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
		showToastEvent("toast_session_disconnected", "🔌 Boot Creator: A BAS website session was disconnected.")
		appendSecurityAudit(securityAuditEvent{Category: "access", Action: "session.disconnected.admin", Actor: actor, Target: "legacy"})
		writeLog("Admin disconnected the active legacy BAS website session.")
		return
	}
	if !disconnectTrustedSessionByID(sessionID) {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Session not found"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
	showToastEvent("toast_session_disconnected", "🔌 Boot Creator: A BAS website session was disconnected.")
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "session.disconnected.admin", Actor: actor, Target: sessionID})
	writeLog("Admin disconnected BAS website session: " + sessionID)
}

func trustSessionsDisconnectAllHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	disconnectAllWebsiteSessions()
	writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
	showToastEvent("toast_sessions_disconnected", "🔌 Boot Creator: All BAS website sessions were disconnected.")
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "session.disconnected.all", Actor: actor})
	writeLog("Admin disconnected all BAS website sessions without changing persistent trust.")
}

func trustPermissionHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	var input trustPermissionInput
	if err := decodeJSONRequest(r, 8<<10, &input); err != nil || !clientIDPattern.MatchString(strings.TrimSpace(input.ClientID)) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client permission request"})
		return
	}
	permission, ok := normalizeTrustPermission(input.Permission)
	if !ok {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client permission"})
		return
	}
	clientID := strings.TrimSpace(input.ClientID)
	if clientID == currentTrustedClientID(r) && permission != "admin" {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "error", Message: "Use another Admin client to change this browser's access level"})
		return
	}
	trustMu.Lock()
	state, err := loadTrustStateLocked()
	found := false
	if err == nil {
		for i := range state.Clients {
			if state.Clients[i].ID == clientID {
				state.Clients[i].Permission = permission
				found = true
				break
			}
		}
		if found {
			err = saveTrustStateLocked(state)
		}
	}
	trustMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not update trusted-client permission"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Trusted client not found"})
		return
	}
	trustedSessionMu.Lock()
	for _, session := range trustedSessions {
		if session.ClientID == clientID {
			session.Permission = permission
		}
	}
	trustedSessionMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "client_id": clientID, "permission": permission})
	showToastEvent("toast_permission_changed", "🔐 Boot Creator: Trusted browser access changed to "+permission+".", permission, "permission")
	recordSecurityAudit(r, "access", "trust.permission.changed", clientID, map[string]string{"permission": permission})
	writeLog("Trusted BAS client permission changed: " + clientID + " -> " + permission)
}

func trustRevokeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	var input trustClientInput
	if err := decodeJSONRequest(r, 8<<10, &input); err != nil || !clientIDPattern.MatchString(strings.TrimSpace(input.ClientID)) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client identity"})
		return
	}
	clientID := strings.TrimSpace(input.ClientID)
	trustMu.Lock()
	state, err := loadTrustStateLocked()
	if err == nil {
		filtered := state.Clients[:0]
		for _, client := range state.Clients {
			if client.ID != clientID {
				filtered = append(filtered, client)
			}
		}
		state.Clients = filtered
		err = saveTrustStateLocked(state)
	}
	trustMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not revoke trusted client"})
		return
	}
	revokeTrustedClientSessions(clientID)
	writeJSON(w, http.StatusOK, statusResponse{Status: "revoked"})
	showToastEvent("toast_trust_revoked", "🔐 Boot Creator: Trusted browser access was revoked.")
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "trust.revoked", Actor: actor, Target: clientID})
	writeLog("Trusted BAS client revoked: " + clientID)
}

func trustRevokeAllHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) || !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	trustMu.Lock()
	state, loadErr := loadTrustStateLocked()
	err := loadErr
	if err == nil {
		err = saveTrustStateLocked(trustState{Version: trustStateVersion, Clients: []trustedClientRecord{}})
	}
	trustMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not revoke trusted clients"})
		return
	}
	for _, client := range state.Clients {
		revokeTrustedClientSessions(client.ID)
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "revoked"})
	showToastEvent("toast_trust_revoked_all", "🔐 Boot Creator: All trusted browser access was revoked.")
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: "trust.revoked.all", Actor: actor})
	writeLog("All persistent BAS trusted clients were revoked. Temporary and legacy sessions were left unchanged.")
}
