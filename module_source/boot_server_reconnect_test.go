package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Distinct auth steps must not exhaust each other's security budgets.
func TestReconnectSeparateChallengeAndApprovalBudgets(t *testing.T) {
	authRateMu.Lock()
	previous := authRateByIP
	authRateByIP = make(map[string]*authRateEntry)
	authRateMu.Unlock()
	t.Cleanup(func() { authRateMu.Lock(); authRateByIP = previous; authRateMu.Unlock() })
	now := time.Date(2026, 11, 4, 4, 0, 0, 0, time.UTC)
	ip := "127.0.0.1"
	for i := 0; i < 4; i++ {
		if ok, _, _ := checkAuthRateLimit(ip, "trusted_challenge", now.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("challenge %d unexpectedly limited", i+1)
		}
		if ok, _, _ := checkAuthRateLimit(ip, "auth_start", now.Add(time.Duration(i)*time.Second+500*time.Millisecond)); !ok {
			t.Fatalf("local reconnect approval %d unexpectedly limited", i+1)
		}
	}
}

func TestReconnectRemoteAbuseStillThrottled(t *testing.T) {
	authRateMu.Lock()
	previous := authRateByIP
	authRateByIP = make(map[string]*authRateEntry)
	authRateMu.Unlock()
	t.Cleanup(func() { authRateMu.Lock(); authRateByIP = previous; authRateMu.Unlock() })
	now := time.Date(2026, 11, 4, 4, 0, 0, 0, time.UTC)
	for i := 0; i < authRateMaxAttempts; i++ {
		if ok, _, _ := checkAuthRateLimit("192.168.1.13", "auth_start", now.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("remote attempt %d denied unexpectedly", i+1)
		}
	}
	if ok, retry, newBlock := checkAuthRateLimit("192.168.1.13", "auth_start", now.Add(4*time.Second)); ok || !newBlock || retry != authRateCooldown {
		t.Fatalf("remote flooding not throttled: allowed %v, new %v, retry %v", ok, newBlock, retry)
	}
}

// Even a same-device caller remains throttled; loopback is not an exemption.
func TestReconnectLocalStillRateLimited(t *testing.T) {
	authRateMu.Lock()
	previous := authRateByIP
	authRateByIP = make(map[string]*authRateEntry)
	authRateMu.Unlock()
	t.Cleanup(func() { authRateMu.Lock(); authRateByIP = previous; authRateMu.Unlock() })
	now := time.Date(2026, 11, 4, 4, 0, 0, 0, time.UTC)
	for i := 0; i < authRateLocalMaxAttempts; i++ {
		if ok, _, _ := checkAuthRateLimit("::1", "auth_start", now.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("local attempt %d blocked", i+1)
		}
	}
	if ok, _, _ := checkAuthRateLimit("::1", "auth_start", now.Add(7*time.Second)); ok {
		t.Fatal("loopback abuse was not rate limited")
	}
}

// The Studio must be able to read Retry-After to tell the user how long to wait.
func TestRateLimitedAuthExposesRetryAfter(t *testing.T) {
	authRateMu.Lock()
	previous := authRateByIP
	authRateByIP = make(map[string]*authRateEntry)
	authRateMu.Unlock()
	t.Cleanup(func() { authRateMu.Lock(); authRateByIP = previous; authRateMu.Unlock() })
	mux := newCompanionMux()
	var last *httptest.ResponseRecorder
	for i := 0; i <= authRateMaxAttempts; i++ {
		req := httptest.NewRequest(http.MethodPost, "https://192.168.1.50:4041/auth/start", strings.NewReader("{}"))
		req.RemoteAddr = "192.168.1.72:50000"
		req.Header.Set("Origin", "https://lumii55.github.io")
		req.Header.Set("Content-Type", "application/json")
		last = httptest.NewRecorder()
		mux.ServeHTTP(last, req)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d = %d, want 429", authRateMaxAttempts+1, last.Code)
	}
	if last.Header().Get("Retry-After") == "" || !strings.Contains(last.Header().Get("Access-Control-Expose-Headers"), "Retry-After") {
		t.Fatalf("Retry-After not readable by the Studio: %v", last.Header())
	}
}
