package main

import (
	"net"
	"net/http"
	"os/exec"
	"time"
)

type pairRegistration struct {
	token   string
	expires time.Time
}

func validPairToken(token string) bool {
	return pairTokenPattern.MatchString(token)
}

func localIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				continue
			}
			ip := ipNet.IP.To4()
			if ip != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

func pairRegisterHandler(w http.ResponseWriter, r *http.Request) {
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

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		http.Error(w, "Invalid pairing token", http.StatusBadRequest)
		return
	}

	stateMu.Lock()
	pendingPair = &pairRegistration{token: token, expires: time.Now().Add(2 * time.Minute)}
	stateMu.Unlock()

	writeJSON(w, http.StatusOK, statusResponse{Status: "ready"})
	phoneIP := localIPv4()
	if phoneIP != "" {
		showToastEvent("toast_qr_approved_ip", "👀 Boot Creator: QR approved. Return to the other device. Phone IP: "+phoneIP, phoneIP)
	} else {
		showToastEvent("toast_qr_approved", "👀 Boot Creator: QR approved. Return to the other device to finish pairing.")
	}
	writeLog("QR pairing token registered locally.")
}

func pairStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "waiting"})
		return
	}

	stateMu.Lock()
	pair := pendingPair
	if pair != nil && time.Now().After(pair.expires) {
		pendingPair = nil
		pair = nil
	}
	ready := pair != nil && secureEqual(pair.token, token)
	stateMu.Unlock()

	if ready {
		writeJSON(w, http.StatusOK, statusResponse{Status: "ready"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "waiting"})
}

func pairExchangeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid pairing token"})
		return
	}

	stateMu.Lock()
	pair := pendingPair
	if pair != nil && time.Now().After(pair.expires) {
		pendingPair = nil
		pair = nil
	}
	if pair == nil || !secureEqual(pair.token, token) {
		stateMu.Unlock()
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "error", Message: "Pairing token not registered"})
		return
	}
	if pendingAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
		return
	}

	nonce, err := randomToken(24)
	if err != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create pairing request"})
		return
	}

	request := &authRequest{nonce: nonce, result: make(chan bool, 1)}
	pendingAuth = request
	stateMu.Unlock()

	defer func() {
		stateMu.Lock()
		if pendingAuth == request {
			pendingAuth = nil
		}
		stateMu.Unlock()
	}()

	clientIP := getIP(r)
	originLabel := originDisplayLabel(r.Header.Get("Origin"))
	command := "am start -n com.bootcreator.companion/.PromptActivity --ez auto_pair true --es nonce " + shellQuote(nonce) + " --es pair_token " + shellQuote(token) + " --es requester_ip " + shellQuote(clientIP) + " --es request_origin " + shellQuote(originLabel)
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not verify pairing approval"})
		writeLog("Error: Failed to verify QR pairing for IP: " + clientIP)
		return
	}

	select {
	case allowed := <-request.result:
		if !allowed {
			writeJSON(w, http.StatusForbidden, statusResponse{Status: "denied"})
			writeLog("QR pairing verification failed for IP: " + clientIP)
			return
		}

		session, err := randomToken(32)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create session"})
			return
		}

		stateMu.Lock()
		pairedIP = clientIP
		pairedToken = session
		pairedCreatedAt = time.Now()
		pairedLastSeen = pairedCreatedAt
		pendingPair = nil
		stateMu.Unlock()

		writeJSON(w, http.StatusOK, statusResponse{
			Status:     "ok",
			Model:      deviceModel,
			Resolution: deviceResolution,
			HasCustom:  checkHasCustomAnim(),
			BridgeID:   bridgeID,
			Token:      session,
		})
		showToastEvent("toast_qr_completed", "✨ Boot Creator: QR pairing completed successfully!")
		emitLiveEvent("sessions", "sessions.changed")
		writeLog("QR pairing completed for IP: " + clientIP)
	case <-time.After(12 * time.Second):
		writeJSON(w, http.StatusRequestTimeout, statusResponse{Status: "timeout"})
		writeLog("QR pairing verification timed out for IP: " + clientIP)
	}
}
