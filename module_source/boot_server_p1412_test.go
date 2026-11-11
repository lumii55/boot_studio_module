package main

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestP1412HTTPListenerIsLoopbackOnly(t *testing.T) {
	srv := newLoopbackCompanionServer(newCompanionMux())
	if srv.Addr != "127.0.0.1:4040" {
		t.Fatalf("plaintext exposed outside loopback: %q", srv.Addr)
	}
	if strings.Contains(srv.Addr, "0.0.0.0") {
		t.Fatal("plaintext wildcard binding")
	}
	// Exercise a real bound TCP socket using the production HTTP handler.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(listener) }()
	defer func() { _ = srv.Close(); <-done }()
	req, _ := http.NewRequest(http.MethodGet, "http://"+listener.Addr().String()+"/ping", nil)
	req.Host = "127.0.0.1:4040" // production Host while socket uses a random test port
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback /ping status %d", resp.StatusCode)
	}
}

func TestP1412RejectRemoteHTTPAndSpoofedHost(t *testing.T) {
	calls := 0
	handler := localCompanionHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) }))
	cases := []struct {
		name, remote, host string
		want               int
	}{
		{"apk-callback-local", "127.0.0.1:12345", "127.0.0.1:4040", 204},
		{"webui-localhost", "127.0.0.1:12345", "localhost:4040", 204},
		{"remote-peer", "192.168.0.40:5555", "127.0.0.1:4040", 403},
		{"remote-nonprivate-peer", "198.51.100.3:5555", "127.0.0.1:4040", 403},
		{"forwarded-ip-spoofed", "192.168.0.40:5555", "127.0.0.1:4040", 403},
		{"dns-rebinding-host", "127.0.0.1:12345", "evil.example:4040", 403},
		{"lan-address-host", "127.0.0.1:12345", "192.168.0.5:4040", 403},
		{"wrong-port", "127.0.0.1:12345", "127.0.0.1:4041", 403},
		{"invalid-peer-address", "nonsense", "127.0.0.1:4040", 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://127.0.0.1:4040/ping", nil)
			req.RemoteAddr, req.Host = tc.remote, tc.host
			// Forwarded headers cannot grant local-only access.
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.want {
				t.Fatalf("got %d want %d", recorder.Code, tc.want)
			}
		})
	}
	if calls != 2 {
		t.Fatalf("backend called %d times instead of 2", calls)
	}
}

func TestP1412WebUIOriginsFollowTransportPolicy(t *testing.T) {
	cases := []struct {
		url     string
		allowed bool
	}{
		{"http://127.0.0.1:4040", true},
		{"http://localhost:4040", true},
		{"https://127.0.0.1:4041", true},
		{"http://192.168.0.13:4040", false},
		{"http://localhost:4041", false},
		{"https://localhost:4040", false},
	}
	for _, tc := range cases {
		u, _ := http.NewRequest("GET", tc.url, nil)
		if got := isModuleWebUIOrigin(u.URL); got != tc.allowed {
			t.Errorf("%s: %v want %v", tc.url, got, tc.allowed)
		}
	}
}

func TestP1412HTTPSFailureLeavesNoRemoteHTTPFallback(t *testing.T) {
	dir := t.TempDir()
	blockingFile := filepath.Join(dir, "regular-file-not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := startSecureTransportServer(newCompanionMux(), blockingFile, nil, time.Now())
	if err == nil {
		t.Fatal("HTTPS unexpectedly started with an invalid key directory")
	}
	srv := newLoopbackCompanionServer(newCompanionMux())
	if srv.Addr != "127.0.0.1:4040" {
		t.Fatalf("HTTP fallback is not loopback-only: %s", srv.Addr)
	}
}

func TestP1412APKCallbacksRemainRegisteredLocally(t *testing.T) {
	mux := newCompanionMux()
	for _, path := range []string{"/pair_register", "/auth_callback", "/webui/", "/webui/request_auth"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4040"+path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		_, pattern := mux.Handler(req)
		if pattern == "" {
			t.Fatalf("APK/WebUI callback route absent: %s", path)
		}
		recorder := httptest.NewRecorder()
		localCompanionHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("callback %s inaccessible through local listener", path)
		}
	}
}

// A trusted certificate is required for the browser-to-phone HTTPS endpoint.
// This test uses httptest instead of opening Android's well-known port.
func TestP1412TLS13RequiresCertificateTrustAndNoUnauthenticatedPrivilege(t *testing.T) {
	cert, _, err := ensureTransportCertificate(t.TempDir(), []net.IP{net.ParseIP("127.0.0.1")}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(newCompanionMux())
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()

	// Default TLS client must reject an untrusted self-signed identity.
	noTrust := &http.Client{Timeout: 3 * time.Second}
	resp, err := noTrust.Get(server.URL + "/ping")
	if err == nil {
		resp.Body.Close()
		t.Fatal("default browser-like client trusted the self-signed leaf unexpectedly")
	}

	roots := x509.NewCertPool()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(leaf)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}}
	resp, err = client.Get(server.URL + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	if resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
		t.Fatal("TLS 1.3 was not negotiated")
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("trusted TLS /ping: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/factory_reset", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("untrusted privileged request: %d", resp.StatusCode)
	}
}
