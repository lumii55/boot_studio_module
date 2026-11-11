package main

import (
	"net/http"
	"strings"
	"time"
)

func normalizeDisconnectReason(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "page_unload":
		return "page_unload"
	default:
		return "manual"
	}
}

func disconnectFeedback(reason string) (string, string, string) {
	if normalizeDisconnectReason(reason) == "page_unload" {
		return "toast_disconnected_page", "🔌 Boot Creator: Website closed or reloaded. Session ended.", "session.disconnected.page_unload"
	}
	return "toast_disconnected_manual", "🔌 Boot Creator: Website disconnected manually.", "session.disconnected.self"
}

func disconnectWebsiteSessionByToken(clientIP, token string) (securityAuditActor, string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return securityAuditActor{}, "", false
	}
	key := trustedSessionKey(token)
	trustedSessionMu.Lock()
	if session := trustedSessions[key]; session != nil && session.ClientIP == clientIP {
		actor := securityAuditActor{Type: "trusted", ClientID: session.ClientID, Label: session.ClientLabel, Permission: session.Permission, ClientIP: session.ClientIP}
		delete(trustedSessions, key)
		trustedSessionMu.Unlock()
		return actor, "trusted", true
	}
	trustedSessionMu.Unlock()

	stateMu.Lock()
	legacy := pairedIP != "" && pairedIP == clientIP && secureEqual(pairedToken, token)
	if legacy {
		pairedIP = ""
		pairedToken = ""
		pairedCreatedAt = time.Time{}
		pairedLastSeen = time.Time{}
		pendingAsyncAuth = nil
	}
	stateMu.Unlock()
	if legacy {
		return securityAuditActor{Type: "legacy", Label: "Legacy BAS website", Permission: "admin", ClientIP: clientIP}, "legacy", true
	}
	return securityAuditActor{}, "", false
}

func disconnectBeaconHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid disconnect request"})
		return
	}
	actor, kind, ok := disconnectWebsiteSessionByToken(getIP(r), r.FormValue("token"))
	if !ok {
		writeJSON(w, http.StatusUnauthorized, statusResponse{Status: "error", Message: "Access denied"})
		return
	}
	if kind == "legacy" {
		if err := clearStagedTestState(); err != nil {
			writeLog("Warning: Could not fully clear Test Lab staging during page disconnect: " + err.Error())
		}
	}
	key, fallback, action := disconnectFeedback(r.FormValue("reason"))
	w.WriteHeader(http.StatusNoContent)
	showToastEvent(key, fallback)
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: action, Actor: actor, Target: kind, Details: map[string]string{"reason": normalizeDisconnectReason(r.FormValue("reason"))}})
	emitLiveEvent("sessions", "sessions.changed")
	writeLog("BAS website session ended through unload-safe disconnect (" + kind + ").")
}

func disconnectHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	actor := securityAuditActorForRequest(r)
	reason := normalizeDisconnectReason(r.URL.Query().Get("reason"))
	toastKey, toastFallback, auditAction := disconnectFeedback(reason)

	if revokeTrustedSessionFromRequest(r) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
		showToastEvent(toastKey, toastFallback)
		appendSecurityAudit(securityAuditEvent{Category: "access", Action: auditAction, Actor: actor, Details: map[string]string{"reason": reason}})
		emitLiveEvent("sessions", "sessions.changed")
		if reason == "page_unload" {
			writeLog("Trusted BAS client session ended because the website was closed or reloaded.")
		} else {
			writeLog("Trusted BAS client session disconnected manually without affecting other clients.")
		}
		return
	}

	if err := clearStagedTestState(); err != nil {
		writeLog("Warning: Could not fully clear Test Lab staging during disconnect: " + err.Error())
	}

	stateMu.Lock()
	pairedIP = ""
	pairedToken = ""
	pairedCreatedAt = time.Time{}
	pairedLastSeen = time.Time{}
	pendingAsyncAuth = nil
	stateMu.Unlock()

	writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
	showToastEvent(toastKey, toastFallback)
	appendSecurityAudit(securityAuditEvent{Category: "access", Action: auditAction, Actor: actor, Target: "legacy", Details: map[string]string{"reason": reason}})
	emitLiveEvent("sessions", "sessions.changed")
	if reason == "page_unload" {
		writeLog("Legacy BAS website session ended because the website was closed or reloaded. Test Lab staging cleared.")
	} else {
		writeLog("Website disconnected manually by user. Test Lab staging cleared.")
	}
}
