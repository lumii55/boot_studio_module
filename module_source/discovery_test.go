package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func discoveryRequest(method, path, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://192.168.1.50:4042"+path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	discoveryHandler(rec, req)
	return rec
}

func TestDiscoveryAnswersOnlyTheFixedSignal(t *testing.T) {
	rec := discoveryRequest(http.MethodGet, "/discover", "https://lumii55.github.io")
	if rec.Code != http.StatusOK || rec.Body.String() != `{"service":"bas-companion","api_version":1,"secure_port":4041}` {
		t.Fatalf("GET /discover = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://lumii55.github.io" {
		t.Fatalf("allowed Studio origin did not get CORS: %v", rec.Header())
	}
	body := strings.ToLower(rec.Body.String())
	for _, leak := range []string{"model", "bridge", "bc_", "version\":\"", "feature", "token", strings.ToLower(deviceModel)} {
		if strings.Contains(body, leak) {
			t.Fatalf("discovery signal leaks %q: %s", leak, body)
		}
	}
	if rec := discoveryRequest(http.MethodGet, "/discover", ""); rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("no-Origin request: %d, CORS %q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestDiscoveryRefusesOtherWebsitesAndMethods(t *testing.T) {
	if rec := discoveryRequest(http.MethodGet, "/discover", "https://evil.example"); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "bas-companion") {
		t.Fatalf("foreign origin: %d %q", rec.Code, rec.Body.String())
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if rec := discoveryRequest(method, "/discover", "https://lumii55.github.io"); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /discover = %d, want 405", method, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodOptions, "http://192.168.1.50:4042/discover", nil)
	req.Header.Set("Origin", "https://lumii55.github.io")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	rec := httptest.NewRecorder()
	discoveryHandler(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatalf("private-network preflight: %d %v", rec.Code, rec.Header())
	}
}

// Structural guarantee: no Companion route is reachable through the discovery port.
func TestDiscoveryNeverServesCompanionRoutes(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	routes := regexp.MustCompile(`HandleFunc\("([^"]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(routes) < 80 {
		t.Fatalf("expected the full Companion route table, found %d routes", len(routes))
	}
	for _, m := range routes {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			rec := discoveryRequest(method, m[1], "https://lumii55.github.io")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s on the discovery port = %d, want 404", method, m[1], rec.Code)
			}
		}
	}
	for _, path := range []string{"/", "/discover/", "/discover/../info", "/DISCOVER"} {
		if rec := discoveryRequest(http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestDiscoveryServerLimits(t *testing.T) {
	server := newDiscoveryServer()
	if server.Addr != "0.0.0.0:4042" || server.MaxHeaderBytes > 2<<10 || server.ReadHeaderTimeout > 3*time.Second || server.WriteTimeout > 5*time.Second {
		t.Fatalf("discovery server limits loosened: %+v", server)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	base := "http://" + listener.Addr().String()
	resp, err := http.Get(base + "/discover")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"secure_port":4041`) {
		t.Fatalf("live /discover = %d %q", resp.StatusCode, body)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/discover", nil)
	// MaxHeaderBytes is 2 KiB; Go adds its own read slack, so anything >= 16 KiB is refused.
	req.Header.Set("X-Padding", strings.Repeat("a", 64<<10))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("oversized headers = %d, want 431", resp.StatusCode)
	}
}

func TestDiscoveryCanBeDisabledByOwner(t *testing.T) {
	dir := t.TempDir()
	if discoveryDisabledIn(dir) {
		t.Fatal("disabled without the flag file")
	}
	if err := os.WriteFile(filepath.Join(dir, "discovery.disabled"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !discoveryDisabledIn(dir) {
		t.Fatal("discovery.disabled flag ignored")
	}
}
