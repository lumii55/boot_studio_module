package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A second request from the same IP must not receive an existing pending or
// approved authorization's request_id. IP addresses do not identify a browser
// tab, app, or individual requester.
func TestAsyncAuthorizationDoesNotDiscloseRequestIDToSameIP(t *testing.T) {
	for _, status := range []string{"pending", "processing", "ok"} {
		t.Run(status, func(t *testing.T) {
			stateMu.Lock()
			previous := pendingAsyncAuth
			pendingAsyncAuth = &asyncAuthRequest{
				nonce: "private-victim-request-id", clientIP: "192.0.2.44",
				clientID: "", status: status, token: "private-approved-token",
				expires: time.Now().Add(time.Minute),
			}
			stateMu.Unlock()
			t.Cleanup(func() {
				stateMu.Lock()
				pendingAsyncAuth = previous
				stateMu.Unlock()
			})

			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4040/auth/start", nil)
			req.RemoteAddr = "192.0.2.44:50000"
			req.Header.Set("Origin", "https://lumii55.github.io")
			recorder := httptest.NewRecorder()
			authStartHandler(recorder, req)
			if recorder.Code != http.StatusConflict {
				t.Fatalf("same-IP request gained access to active approval: status=%d response=%q", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "private-victim-request-id") {
				t.Fatal("private authorization request ID leaked to a second request")
			}
		})
	}
}

// A localhost page served by another process/port is same-site to Chromium,
// so SameSite=Strict does not protect the WebUI's privileged cookie by itself.
func TestWebUICookieRequiresSameOrigin(t *testing.T) {
	const token = "private-local-webui-cookie"
	webUISessionMu.Lock()
	previous := webUISessions
	webUISessions = map[string]time.Time{token: time.Now().Add(time.Hour)}
	webUISessionMu.Unlock()
	t.Cleanup(func() {
		webUISessionMu.Lock()
		webUISessions = previous
		webUISessionMu.Unlock()
	})

	cases := []struct {
		name, origin, fetchSite, host string
		authorized                    bool
	}{
		{"same-origin", "http://127.0.0.1:4040", "same-origin", "127.0.0.1:4040", true},
		{"legacy-webview-no-origin", "", "", "127.0.0.1:4040", true},
		{"other-local-port", "http://127.0.0.1:5173", "same-site", "127.0.0.1:4040", false},
		{"other-local-port-without-fetch-metadata", "http://127.0.0.1:5173", "", "127.0.0.1:4040", false},
		{"cross-port-no-origin", "", "same-site", "127.0.0.1:4040", false},
		{"spoofed-host", "", "", "unrelated.test:4040", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/ping", nil)
			req.RemoteAddr = "127.0.0.1:50001"
			req.AddCookie(&http.Cookie{Name: webUISessionCookieName, Value: token})
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			_, authorized := requestAuthorizationPermission(req)
			if authorized != tc.authorized {
				state, _ := json.Marshal(map[string]any{"origin": tc.origin, "fetchSite": tc.fetchSite, "host": tc.host})
				t.Fatalf("authorization=%v wanted=%v for %s", authorized, tc.authorized, state)
			}
		})
	}
}
