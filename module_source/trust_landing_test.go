package main

import (
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestTrustLandingReturnValidation(t *testing.T) {
	studio := "https://lumii55.github.io/boot_animation_studio/"
	cases := []struct {
		raw, wantURL, wantOrigin string
	}{
		{studio, studio, "https://lumii55.github.io"},
		{studio + "#bas-secure", studio, "https://lumii55.github.io"},
		{"http://127.0.0.1:8080/site/", "http://127.0.0.1:8080/site/", "http://127.0.0.1:8080"},
		{"http://localhost:5173/", "http://localhost:5173/", "http://localhost:5173"},
		{"", "", ""},
		{"https://evil.example/", "", ""},
		{"https://lumii55.github.io.evil.example/", "", ""},
		{"http://lumii55.github.io/boot_animation_studio/", "", ""}, // Studio must be HTTPS
		{"https://user@lumii55.github.io/", "", ""},
		{"javascript:alert(1)", "", ""},
		{"//lumii55.github.io/", "", ""},
		{"http://192.168.1.20:4040/webui/", "", ""}, // module WebUI is not a Studio
		{"https://lumii55.github.io/" + strings.Repeat("a", 2100), "", ""},
	}
	for _, c := range cases {
		gotURL, gotOrigin := validatedTrustReturn(c.raw)
		if gotURL != c.wantURL || gotOrigin != c.wantOrigin {
			t.Errorf("validatedTrustReturn(%q) = (%q, %q), want (%q, %q)", c.raw, gotURL, gotOrigin, c.wantURL, c.wantOrigin)
		}
	}
}

func TestTrustLandingPageIsStaticAndLockedDown(t *testing.T) {
	mux := newCompanionMux()
	studio := "https://lumii55.github.io/boot_animation_studio/"
	req := httptest.NewRequest(http.MethodGet, "/trust?return="+url.QueryEscape(studio), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-origin="https://lumii55.github.io"`) || !strings.Contains(body, `data-return="`+studio+`"`) {
		t.Fatalf("validated return not embedded: %s", body)
	}
	// No device data on an unauthenticated page.
	for _, leak := range []string{"token", "fingerprint", "bridge", "bc_", deviceModel, deviceResolution} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(leak)) {
			t.Fatalf("landing page leaks %q", leak)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	sum := sha256.Sum256([]byte(trustLandingScript))
	if !strings.Contains(csp, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") || !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP does not pin the inline script or lock the page down: %s", csp)
	}
	if !strings.Contains(body, "<script>"+trustLandingScript+"</script>") {
		t.Fatal("served script differs from the hashed script")
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing cache/frame headers: %v", rec.Header())
	}

	// Hostile return value: dropped, and attribute escaping holds.
	req = httptest.NewRequest(http.MethodGet, "/trust?return="+url.QueryEscape(`https://evil.example/"><script>alert(1)</script>`), nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "evil.example") || !strings.Contains(rec.Body.String(), `data-origin="" data-return=""`) {
		t.Fatalf("hostile return leaked into the page: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/trust", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /trust = %d, want 405", rec.Code)
	}
}

func TestWebUILANAddressesListsOnlyPrivateIPv4(t *testing.T) {
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.1.50"), net.ParseIP("8.8.8.8"), net.ParseIP("fe80::1"), net.ParseIP("10.0.0.7"), net.ParseIP("192.168.1.50")}
	if got := webUILANAddresses(ips); got != "10.0.0.7  ·  192.168.1.50" {
		t.Fatalf("webUILANAddresses = %q", got)
	}
	if got := webUILANAddresses(nil); got != "—" {
		t.Fatalf("empty = %q", got)
	}
}

func TestWebUIPageHasNoUnfilledPlaceholders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4040/webui/", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:4040"
	rec := httptest.NewRecorder()
	webUIRootHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<!-- BAS_") {
		t.Fatal("WebUI left a server placeholder unfilled")
	}
}
