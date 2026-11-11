package main

import (
	"bytes"
	_ "embed"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed webui/console.html
var webUIHTML string

type webUILogsResponse struct {
	Status        string `json:"status"`
	Current       string `json:"current"`
	Previous      string `json:"previous"`
	CurrentBytes  int64  `json:"current_bytes"`
	PreviousBytes int64  `json:"previous_bytes"`
}

type webUISessionResponse struct {
	Status     string `json:"status"`
	Authorized bool   `json:"authorized"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
}

func isModuleWebUIOrigin(u *url.URL) bool {
	if u == nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme == "http" {
		// P14.12: the on-device WebUI no longer has an HTTP LAN origin.
		return u.Port() == "4040" && loopback
	}
	if u.Scheme != "https" || u.Port() != "4041" {
		return false
	}
	return loopback || isLocalDeviceIPString(host)
}

func cleanupWebUISessionsLocked(now time.Time) {
	for token, expires := range webUISessions {
		if !expires.After(now) {
			delete(webUISessions, token)
			delete(webUISecureSessions, token)
		}
	}
	if len(webUISessions) <= 12 {
		return
	}
	type sessionExpiry struct {
		token   string
		expires time.Time
	}
	items := make([]sessionExpiry, 0, len(webUISessions))
	for token, expires := range webUISessions {
		items = append(items, sessionExpiry{token: token, expires: expires})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].expires.Before(items[j].expires) })
	for len(webUISessions) > 12 && len(items) > 0 {
		delete(webUISessions, items[0].token)
		delete(webUISecureSessions, items[0].token)
		items = items[1:]
	}
}

func createWebUISession(w http.ResponseWriter, r *http.Request) (time.Time, error) {
	token, err := randomToken(32)
	if err != nil {
		return time.Time{}, err
	}
	expires := time.Now().Add(webUISessionTTL)
	webUISessionMu.Lock()
	cleanupWebUISessionsLocked(time.Now())
	webUISessions[token] = expires
	if r.TLS != nil {
		webUISecureSessions[token] = true
	}
	webUISessionMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     webUISessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   int(webUISessionTTL.Seconds()),
	})
	return expires, nil
}

func webUISessionExpiry(r *http.Request) (time.Time, bool) {
	if !isLocalDeviceRequest(r) {
		return time.Time{}, false
	}
	cookie, err := r.Cookie(webUISessionCookieName)
	if err != nil || cookie.Value == "" {
		return time.Time{}, false
	}
	now := time.Now()
	webUISessionMu.Lock()
	defer webUISessionMu.Unlock()
	cleanupWebUISessionsLocked(now)
	expires, ok := webUISessions[cookie.Value]
	// A TLS-issued admin session must not be replayable over the legacy HTTP
	// port, even when a caller manually supplies its cookie value.
	return expires, ok && expires.After(now) && (!webUISecureSessions[cookie.Value] || r.TLS != nil)
}

func clearWebUISession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(webUISessionCookieName); err == nil && cookie.Value != "" {
		webUISessionMu.Lock()
		delete(webUISessions, cookie.Value)
		delete(webUISecureSessions, cookie.Value)
		webUISessionMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     webUISessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// The WebUI cookie grants admin access, so unlike a bearer token it must not
// authorize a second localhost website. Ports are excluded from browser
// SameSite calculations: http://127.0.0.1:5173 is same-site with :4040 but is
// NOT our WebUI. Keep legacy WebViews (without Fetch Metadata) compatible,
// while requiring an exact origin when one is provided.
func isSameOriginWebUIRequest(r *http.Request) bool {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if !isModuleWebUIOrigin(&url.URL{Scheme: scheme, Host: r.Host}) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != scheme+"://"+r.Host {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "none", "same-origin":
		return true
	default:
		return false
	}
}

func isWebUIAuthorized(r *http.Request) bool {
	if !isSameOriginWebUIRequest(r) {
		return false
	}
	_, ok := webUISessionExpiry(r)
	return ok
}

func webUIRootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/webui" && r.URL.Path != "/webui/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isLocalDeviceRequest(r) {
		http.Error(w, "Module WebUI is local-device only", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	// Transport fingerprint is displayed by the *local* WebUI as a manual
	// verification channel. Never trust a fingerprint fetched over intercepted HTTP.
	identity := currentTransportFingerprint()
	if identity == "" {
		identity = "UNAVAILABLE: HTTPS did not start; check the module log"
	}
	page := strings.Replace(webUIHTML, "<!-- BAS_TLS_FINGERPRINT -->", html.EscapeString(identity), 1)
	page = strings.Replace(page, "<!-- BAS_LAN_ADDRESSES -->", html.EscapeString(webUILANAddresses(localDeviceIPs())), 1)
	_, _ = io.WriteString(w, page)
}

// webUILANAddresses lists the private IPv4 addresses other devices on the Wi-Fi can type
// into the Studio to reach this phone over HTTPS. Shown only by the local-only WebUI.
func webUILANAddresses(ips []net.IP) string {
	seen := map[string]bool{}
	var out []string
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil || !v4.IsPrivate() || seen[v4.String()] {
			continue
		}
		seen[v4.String()] = true
		out = append(out, v4.String())
	}
	if len(out) == 0 {
		return "—"
	}
	sort.Strings(out)
	return strings.Join(out, "  ·  ")
}

func webUISessionHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !isLocalDeviceRequest(r) {
		writeJSON(w, http.StatusForbidden, webUISessionResponse{Status: "local_only", Authorized: false})
		return
	}
	expires, ok := webUISessionExpiry(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, webUISessionResponse{Status: "auth_required", Authorized: false})
		return
	}
	writeJSON(w, http.StatusOK, webUISessionResponse{Status: "ok", Authorized: true, ExpiresAt: expires.UnixMilli()})
}

func webUIRequestAuthHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !isLocalDeviceRequest(r) {
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "error", Message: "Module WebUI is local-device only"})
		return
	}
	if !enforceAuthRateLimit(w, r, "webui") {
		return
	}

	clientIP := getIP(r)
	nonce, err := randomToken(24)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create WebUI authorization request"})
		return
	}
	request := &authRequest{nonce: nonce, result: make(chan bool, 1)}
	stateMu.Lock()
	if pendingAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy", Message: "Another authorization request is active"})
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

	command := "am start -n com.bootcreator.companion/.PromptActivity --es nonce " + shellQuote(nonce) + " --es requester_ip " + shellQuote(clientIP) + " --es request_origin " + shellQuote("Module WebUI")
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not open WebUI authorization prompt"})
		writeLog("Error: Failed to launch Module WebUI authorization prompt.")
		return
	}

	select {
	case allowed := <-request.result:
		if !allowed {
			writeJSON(w, http.StatusForbidden, statusResponse{Status: "denied"})
			writeLog("Module WebUI authorization denied on device.")
			return
		}
		expires, err := createWebUISession(w, r)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create WebUI session"})
			return
		}
		writeLog("Module WebUI authorized locally until " + expires.Format(time.RFC3339) + ".")
		writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
	case <-time.After(30 * time.Second):
		writeJSON(w, http.StatusRequestTimeout, statusResponse{Status: "timeout"})
		writeLog("Module WebUI authorization prompt timed out.")
	}
}

func webUILogoutHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !isLocalDeviceRequest(r) {
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "error", Message: "Module WebUI is local-device only"})
		return
	}
	clearWebUISession(w, r)
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
	writeLog("Module WebUI local session ended.")
}

func readWebUILogTail(path string) (string, int64) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", 0
	}
	size := info.Size()
	start := int64(0)
	if size > webUILogTailBytes {
		start = size - webUILogTailBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return "", size
	}
	defer f.Close()
	if start > 0 {
		_, _ = f.Seek(start, io.SeekStart)
	}
	data, err := io.ReadAll(io.LimitReader(f, webUILogTailBytes+1))
	if err != nil {
		return "", size
	}
	if start > 0 {
		if index := bytes.IndexByte(data, '\n'); index >= 0 && index+1 < len(data) {
			data = data[index+1:]
		}
	}
	return string(data), size
}

func webUILogsHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	current, currentBytes := readWebUILogTail(filepath.Join(ModDir, "boot_creator.log"))
	previous, previousBytes := readWebUILogTail(filepath.Join(ModDir, "boot_creator.previous.log"))
	writeJSON(w, http.StatusOK, webUILogsResponse{Status: "ok", Current: current, Previous: previous, CurrentBytes: currentBytes, PreviousBytes: previousBytes})
}

func webUILogsDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) || !requireAuthorization(w, r) {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "both"
	}
	current, _ := readWebUILogTail(filepath.Join(ModDir, "boot_creator.log"))
	previous, _ := readWebUILogTail(filepath.Join(ModDir, "boot_creator.previous.log"))
	var body, filename string
	switch scope {
	case "current":
		body, filename = current, "boot-creator-current.log"
	case "previous":
		body, filename = previous, "boot-creator-previous.log"
	case "both":
		body = "CURRENT BOOT\n===================================\n" + current + "\n\nPREVIOUS BOOT\n===================================\n" + previous
		filename = "boot-creator-logs.txt"
	default:
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid log scope"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	_, _ = io.WriteString(w, body)
}
