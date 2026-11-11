package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestP1411TransportCertificatePersistentAndIPScoped(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	ip := net.ParseIP("192.168.1.7")
	pair, fingerprint, err := ensureTransportCertificate(dir, []net.IP{ip}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(fingerprint) != 64 || len(pair.Certificate) == 0 {
		t.Fatalf("invalid identity %q", fingerprint)
	}
	for _, name := range []string{"server-key.pem", "server-cert.pem"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s: mode %v", name, info.Mode().Perm())
		}
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("private key directory mode: %v / %v", info, err)
	}
	_, again, err := ensureTransportCertificate(dir, []net.IP{ip}, now.Add(time.Hour))
	if err != nil || again != fingerprint {
		t.Fatalf("identity changed without IP change: %s %v", again, err)
	}
	_, renewed, err := ensureTransportCertificate(dir, []net.IP{ip, net.ParseIP("10.0.0.2")}, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if renewed == fingerprint {
		t.Fatal("certificate with missing IP SAN reused")
	}
	for _, ip := range []string{"127.0.0.1", "::1", "localhost", "10.0.0.2"} {
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if ip == "10.0.0.2" {
			continue
		}
		if err := cert.VerifyHostname(ip); err != nil {
			t.Errorf("old certificate missing %s: %v", ip, err)
		}
	}
}

func TestP1411HTTPSRoutesUseSameMuxAndRejectUnauthenticatedActions(t *testing.T) {
	dir := t.TempDir()
	cert, _, err := ensureTransportCertificate(dir, []net.IP{net.ParseIP("127.0.0.1")}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(newCompanionMux())
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	rootPEM, err := os.ReadFile(filepath.Join(dir, "server-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatal("invalid certificate")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}}
	for _, test := range []struct {
		path, method string
		status       int
	}{
		{"/info", http.MethodGet, http.StatusOK},
		{"/ping", http.MethodGet, http.StatusOK},
		{"/remove", http.MethodPost, http.StatusUnauthorized},
		{"/upload", http.MethodPost, http.StatusUnauthorized},
		{"/factory_reset", http.MethodPost, http.StatusUnauthorized},
	} {
		req, err := http.NewRequest(test.method, server.URL+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "https://lumii55.github.io")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != test.status {
			t.Errorf("%s: HTTP %d want %d", test.path, resp.StatusCode, test.status)
		}
		if resp.Header.Get("Access-Control-Allow-Origin") != "https://lumii55.github.io" {
			t.Errorf("%s: CORS mismatch", test.path)
		}
	}
}

func TestP1411SecureWebUICookieCannotAuthorizeDifferentOrigins(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:4041/webui", nil)
	r.TLS = &tls.ConnectionState{}
	_, err := createWebUISession(w, r)
	if err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("TLS WebUI cookie is not Secure+HttpOnly")
	}
	cookie := cookies[0]
	check := func(host, origin, site string, tlsState bool) bool {
		request := httptest.NewRequest(http.MethodGet, "http://"+host+"/ping", nil)
		request.RemoteAddr = "127.0.0.1:10023"
		if tlsState {
			request.TLS = &tls.ConnectionState{}
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if site != "" {
			request.Header.Set("Sec-Fetch-Site", site)
		}
		request.AddCookie(cookie)
		return isWebUIAuthorized(request)
	}
	if !check("127.0.0.1:4041", "https://127.0.0.1:4041", "same-origin", true) {
		t.Fatal("same-origin TLS session was rejected")
	}
	if check("127.0.0.1:4041", "https://127.0.0.1:5173", "same-site", true) {
		t.Fatal("foreign TLS port granted admin")
	}
	if check("127.0.0.1:4040", "http://127.0.0.1:4040", "same-origin", false) {
		t.Fatal("TLS cookie accepted on HTTP (server-side)")
	}
}

func TestP1411OnlyLocalDeviceCanReadTransportIdentity(t *testing.T) {
	former := currentTransportFingerprint()
	transportFingerprint.Store(strings.Repeat("A", 64))
	defer func() { transportFingerprint.Store(former) }()
	for _, tc := range []struct {
		addr string
		want int
	}{{"127.0.0.1:54820", 200}, {"192.0.2.3:54820", 403}} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4040/transport/identity", nil)
		req.RemoteAddr = tc.addr
		w := httptest.NewRecorder()
		transportIdentityHandler(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s status %d != %d", tc.addr, w.Code, tc.want)
		}
		if tc.want == 200 {
			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got["fingerprint_sha256"] != strings.Repeat("A", 64) {
				t.Fatal("bad identity response", err)
			}
		}
	}
}

func TestP1411CompanionRouteContract(t *testing.T) {
	mux := newCompanionMux()
	paths := []string{"/webui", "/info", "/ping", "/auth/start", "/auth/status", "/trust/permission", "/upload", "/history/download", "/playlist/apply", "/rotation/status", "/test/stage", "/transport/identity"}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, "http://localhost:4040"+path, nil)
		_, pattern := mux.Handler(req)
		if pattern == "" {
			t.Errorf("route %s disappeared", path)
		}
	}
}
