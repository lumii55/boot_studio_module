package main

import (
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type authRequest struct {
	nonce  string
	result chan bool
}

type asyncAuthRequest struct {
	nonce       string
	clientIP    string
	clientID    string
	clientLabel string
	publicKey   *trustPublicKey
	status      string
	token       string
	expires     time.Time
}

func requestRequiredPermission(r *http.Request) string {
	path := r.URL.Path
	switch path {
	case "/ping", "/disconnect", "/live/revisions", "/live/events", "/presence/status", "/pull", "/history/list", "/history/items", "/history/download", "/history/preview", "/playlist/list", "/playlist/download", "/rotation/status", "/activity/list", "/device/probes", "/health/status", "/test/status":
		return "view"
	case "/upload", "/remove", "/history/apply", "/playlist/preview", "/playlist/apply", "/playlist/stage", "/test/stage", "/test/start", "/test/stop", "/test/apply", "/test/clear", "/test_anim":
		return "control"
	case "/reset", "/rescan", "/health/action", "/history/delete", "/playlist/create", "/playlist/rename", "/playlist/duplicate", "/playlist/delete", "/playlist/reorder", "/playlist/item/upload", "/playlist/item/from-history", "/playlist/item/from-staged", "/playlist/item/remove", "/playlist/item/reorder", "/rotation/configure", "/rotation/prepare-next", "/rotation/pause", "/queue/add", "/queue/use-next", "/queue/reorder", "/queue/remove", "/queue/clear", "/queue/skip-next", "/activity/clear":
		return "manage"
	case "/trust/clients", "/trust/sessions", "/trust/permission", "/trust/revoke", "/trust/revoke-all", "/trust/session/disconnect", "/trust/sessions/disconnect-all", "/audit/list", "/audit/clear", "/audit/export", "/factory_reset":
		return "admin"
	default:
		return "admin"
	}
}

func requestAuthorizationPermission(r *http.Request) (string, bool) {
	if isWebUIAuthorized(r) {
		return "admin", true
	}
	if session, _, ok := trustedSessionForRequest(r); ok {
		permission, valid := normalizeTrustPermission(session.Permission)
		return permission, valid
	}

	clientIP := getIP(r)
	token := r.Header.Get("X-Boot-Creator-Token")
	stateMu.Lock()
	valid := pairedIP != "" && pairedIP == clientIP && secureEqual(pairedToken, token)
	if valid {
		pairedLastSeen = time.Now()
	}
	stateMu.Unlock()
	if valid {
		return "admin", true
	}
	return "", false
}

func isAuthorized(r *http.Request) bool {
	_, ok := requestAuthorizationPermission(r)
	return ok
}

func requireAuthorization(w http.ResponseWriter, r *http.Request) bool {
	permission, ok := requestAuthorizationPermission(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, statusResponse{Status: "error", Message: "Access denied"})
		return false
	}
	required := requestRequiredPermission(r)
	if trustPermissionRank(permission) < trustPermissionRank(required) {
		writeJSON(w, http.StatusForbidden, map[string]any{"status": "forbidden", "message": "This trusted client does not have permission for this action", "permission": permission, "required_permission": required})
		return false
	}
	return true
}

func authStartHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}

	approval, hasTrustIdentity, err := readOptionalTrustApproval(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid trusted-client identity"})
		return
	}
	clientID := ""
	clientLabel := ""
	var publicKey *trustPublicKey
	if hasTrustIdentity {
		clientID = approval.ClientID
		clientLabel = approval.Label
		publicKey = approval.PublicKey
	}

	clientIP := getIP(r)
	origin := r.Header.Get("Origin")
	now := time.Now()
	if !enforceAuthRateLimit(w, r, "auth_start") {
		return
	}

	stateMu.Lock()
	if pendingAsyncAuth != nil && now.After(pendingAsyncAuth.expires) {
		pendingAsyncAuth = nil
	}
	if pendingAsyncAuth != nil {
		existing := pendingAsyncAuth
		sameRequester := existing.clientIP == clientIP && existing.clientID == clientID
		// The source IP and public client ID are not request secrets. A second
		// app or tab on the same Android device can supply both and must never
		// receive the existing approval's nonce: /auth/status exchanges it for
		// an admin bearer token after approval. A lost /auth/start response
		// therefore requires a fresh user retry after the pending operation.
		if sameRequester && (existing.status == "denied" || existing.status == "timeout" || existing.status == "error") {
			pendingAsyncAuth = nil
		} else {
			stateMu.Unlock()
			writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
			return
		}
	}
	if pendingAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
		return
	}

	nonce, err := randomToken(24)
	if err != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create authorization request"})
		return
	}
	request := &asyncAuthRequest{
		nonce: nonce, clientIP: clientIP, clientID: clientID, clientLabel: clientLabel, publicKey: publicKey,
		status: "pending", expires: now.Add(45 * time.Second),
	}
	pendingAsyncAuth = request
	stateMu.Unlock()

	originLabel := originDisplayLabel(origin)
	command := "am start -n com.bootcreator.companion/.PromptActivity --es nonce " + shellQuote(nonce) + " --es requester_ip " + shellQuote(clientIP) + " --es request_origin " + shellQuote(originLabel)
	if hasTrustIdentity {
		command += " --es client_label " + shellQuote(clientLabel) + " --es client_id " + shellQuote(clientID) + " --ez trust_choice_supported true --ez permission_choice_supported true"
	}
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		stateMu.Lock()
		if pendingAsyncAuth == request {
			pendingAsyncAuth = nil
		}
		stateMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not open authorization prompt"})
		writeLog("Error: Failed to launch asynchronous authorization prompt for IP: " + clientIP)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending", "request_id": nonce})
	if hasTrustIdentity {
		writeLog("Trusted-client approval requested by " + clientLabel + " from IP: " + clientIP)
	} else {
		writeLog("Asynchronous connection approval requested by IP: " + clientIP)
	}
}

func authStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	clientIP := getIP(r)
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if requestID == "" {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Missing authorization request id"})
		return
	}

	stateMu.Lock()
	request := pendingAsyncAuth
	if request == nil || request.clientIP != clientIP || !secureEqual(request.nonce, requestID) {
		stateMu.Unlock()
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Authorization request not found"})
		return
	}
	if time.Now().After(request.expires) && request.status == "pending" {
		request.status = "timeout"
	}
	status := request.status
	token := request.token
	if status == "ok" && request.clientID != "" && pendingAsyncAuth == request {
		pendingAsyncAuth = nil
	}
	stateMu.Unlock()

	switch status {
	case "pending", "processing", "denied", "timeout":
		if status == "processing" {
			status = "pending"
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": status, "request_id": requestID})
	case "ok":
		permission := "admin"
		if request.clientID != "" {
			if session, _, ok := trustedSessionForToken(request.clientID, request.clientIP, token); ok {
				permission = session.Permission
			}
		}
		writeJSON(w, http.StatusOK, statusResponse{
			Status:     "ok",
			Model:      deviceModel,
			Resolution: deviceResolution,
			HasCustom:  checkHasCustomAnim(),
			BridgeID:   bridgeID,
			Token:      token,
			Permission: permission,
		})
	default:
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Invalid authorization state"})
	}
}

func requestAuthHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}

	clientIP := getIP(r)
	origin := r.Header.Get("Origin")
	if !enforceAuthRateLimit(w, r, "legacy_auth") {
		return
	}
	nonce, err := randomToken(24)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create authorization request"})
		writeLog("Error: Failed to generate authorization nonce.")
		return
	}

	request := &authRequest{
		nonce:  nonce,
		result: make(chan bool, 1),
	}

	stateMu.Lock()
	if pendingAuth != nil || pendingAsyncAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
		return
	}
	pendingAuth = request
	stateMu.Unlock()

	defer func() {
		stateMu.Lock()
		if pendingAuth == request {
			pendingAuth = nil
		}
		stateMu.Unlock()
	}()

	originLabel := originDisplayLabel(origin)

	command := "am start -n com.bootcreator.companion/.PromptActivity --es nonce " + shellQuote(nonce) + " --es requester_ip " + shellQuote(clientIP) + " --es request_origin " + shellQuote(originLabel)
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not open authorization prompt"})
		writeLog("Error: Failed to launch authorization prompt for IP: " + clientIP)
		return
	}

	select {
	case allowed := <-request.result:
		if !allowed {
			writeJSON(w, http.StatusForbidden, statusResponse{Status: "denied"})
			showToastEvent("toast_connection_denied", "❌ Boot Creator: Connection denied by user.")
			recordCompanionSecurityAudit("auth.denied", "legacy", "Legacy BAS website", clientIP, nil)
			writeLog("Connection denied by user from IP: " + clientIP)
			return
		}

		token, err := randomToken(32)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create session"})
			writeLog("Error: Failed to generate session token for IP: " + clientIP)
			return
		}

		stateMu.Lock()
		pairedIP = clientIP
		pairedToken = token
		pairedCreatedAt = time.Now()
		pairedLastSeen = pairedCreatedAt
		stateMu.Unlock()

		writeJSON(w, http.StatusOK, statusResponse{
			Status:     "ok",
			Model:      deviceModel,
			Resolution: deviceResolution,
			HasCustom:  checkHasCustomAnim(),
			BridgeID:   bridgeID,
			Token:      token,
		})
		showToastEvent("toast_connected", "✨ Boot Creator: Website connected successfully!")
		recordCompanionSecurityAudit("auth.approved", "legacy", "Legacy BAS website", clientIP, map[string]string{"permission": "admin", "persistent": "false"})
		emitLiveEvent("sessions", "sessions.changed")
		writeLog("Connection allowed by user from IP: " + clientIP)
	case <-time.After(30 * time.Second):
		writeJSON(w, http.StatusRequestTimeout, statusResponse{Status: "timeout"})
		writeLog("Connection prompt timed out for IP: " + clientIP)
	}
}

func authCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := net.ParseIP(getIP(r))
	if clientIP == nil || !clientIP.IsLoopback() {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	nonce := r.URL.Query().Get("nonce")
	allowValue := r.URL.Query().Get("allow")
	persistValue := r.URL.Query().Get("persist")
	permissionValue := strings.TrimSpace(r.URL.Query().Get("permission"))
	if permissionValue == "" {
		permissionValue = "admin"
	}
	permissionValue, permissionValid := normalizeTrustPermission(permissionValue)
	if nonce == "" || (allowValue != "true" && allowValue != "false") || (persistValue != "" && persistValue != "true" && persistValue != "false") || !permissionValid {
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}

	stateMu.Lock()
	asyncRequest := pendingAsyncAuth
	if asyncRequest != nil && secureEqual(asyncRequest.nonce, nonce) {
		if time.Now().After(asyncRequest.expires) {
			if asyncRequest.status == "pending" {
				asyncRequest.status = "timeout"
			}
			stateMu.Unlock()
			http.Error(w, "Authorization request expired", http.StatusGone)
			return
		}
		if asyncRequest.status != "pending" {
			stateMu.Unlock()
			http.Error(w, "Authorization request already completed", http.StatusConflict)
			return
		}
		if allowValue == "false" {
			deniedIP := asyncRequest.clientIP
			asyncRequest.status = "denied"
			stateMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			showToastEvent("toast_connection_denied", "❌ Boot Creator: Connection denied by user.")
			recordCompanionSecurityAudit("auth.denied", asyncRequest.clientID, asyncRequest.clientLabel, deniedIP, nil)
			writeLog("Asynchronous connection denied by user from IP: " + deniedIP)
			return
		}

		asyncRequest.status = "processing"
		approvedIP := asyncRequest.clientIP
		approvedClientID := asyncRequest.clientID
		approvedLabel := asyncRequest.clientLabel
		approvedKey := asyncRequest.publicKey
		stateMu.Unlock()

		var token string
		var err error
		if approvedClientID != "" && approvedKey != nil {
			persistTrust := persistValue == "" || persistValue == "true"
			approval := trustApprovalInput{ClientID: approvedClientID, Label: approvedLabel, PublicKey: approvedKey}
			permission := permissionValue
			if persistTrust {
				if err = upsertTrustedClient(approval, approvedIP, permission); err == nil {
					token, err = issueTrustedSession(approvedClientID, approvedLabel, approvedIP, permission, true)
				}
			} else {
				token, err = issueTrustedSession(approvedClientID, approvedLabel, approvedIP, permission, false)
			}
		} else {
			token, err = randomToken(32)
			if err == nil {
				stateMu.Lock()
				pairedIP = approvedIP
				pairedToken = token
				pairedCreatedAt = time.Now()
				pairedLastSeen = pairedCreatedAt
				stateMu.Unlock()
			}
		}
		if err != nil {
			stateMu.Lock()
			if pendingAsyncAuth == asyncRequest {
				asyncRequest.status = "error"
			}
			stateMu.Unlock()
			http.Error(w, "Could not create session", http.StatusInternalServerError)
			writeLog("Error: Failed to create approved website session: " + err.Error())
			return
		}

		stateMu.Lock()
		if pendingAsyncAuth == asyncRequest {
			asyncRequest.token = token
			asyncRequest.status = "ok"
			asyncRequest.expires = time.Now().Add(60 * time.Second)
		}
		stateMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		showToastEvent("toast_connected", "✨ Boot Creator: Website connected successfully!")
		if approvedClientID != "" {
			persistAudit := "true"
			if persistValue == "false" {
				persistAudit = "false"
			}
			recordCompanionSecurityAudit("auth.approved", approvedClientID, approvedLabel, approvedIP, map[string]string{"permission": permissionValue, "persistent": persistAudit})
			if persistValue == "false" {
				writeLog("BAS client approved for this session only: " + cleanClientLabel(approvedLabel) + " from IP: " + approvedIP)
			} else {
				writeLog("Trusted BAS client approved persistently: " + cleanClientLabel(approvedLabel) + " from IP: " + approvedIP)
			}
		} else {
			writeLog("Asynchronous connection allowed by user from IP: " + approvedIP)
		}
		emitLiveEvent("sessions", "sessions.changed")
		return
	}

	request := pendingAuth
	valid := request != nil && secureEqual(request.nonce, nonce)
	stateMu.Unlock()

	if !valid {
		http.Error(w, "Authorization request not found", http.StatusForbidden)
		return
	}

	select {
	case request.result <- allowValue == "true":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Authorization request already completed", http.StatusConflict)
	}
}
