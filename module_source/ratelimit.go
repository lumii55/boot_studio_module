package main

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type authRateEntry struct {
	Attempts     []time.Time
	BlockedUntil time.Time
	LastSeen     time.Time
}

func checkAuthRateLimit(clientIP, scope string, now time.Time) (bool, time.Duration, bool) {
	clientIP = strings.TrimSpace(clientIP)
	if clientIP == "" {
		clientIP = "unknown"
	}
	authRateMu.Lock()
	defer authRateMu.Unlock()

	for key, entry := range authRateByIP {
		if entry == nil || (now.Sub(entry.LastSeen) > authRateRetention && !entry.BlockedUntil.After(now)) {
			delete(authRateByIP, key)
		}
	}
	// Identity challenges and approval prompts are independent operations.
	// Sharing a single budget caused normal loopback reconnects to be blocked.
	key := scope + "\x00" + clientIP
	entry := authRateByIP[key]
	if entry == nil {
		entry = &authRateEntry{}
		authRateByIP[key] = entry
	}
	entry.LastSeen = now
	if entry.BlockedUntil.After(now) {
		return false, entry.BlockedUntil.Sub(now), false
	}
	cutoff := now.Add(-authRateWindow)
	kept := entry.Attempts[:0]
	for _, attempt := range entry.Attempts {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	entry.Attempts = kept
	maxAttempts := authRateMaxAttempts
	if addr := net.ParseIP(clientIP); addr != nil && addr.IsLoopback() {
		// Only same-device traffic receives a larger reconnect budget.
		// Other LAN clients retain strict throttling. Source IP comes from
		// RemoteAddr, never from a client-supplied forwarding header.
		maxAttempts = authRateLocalMaxAttempts
	}
	if len(entry.Attempts) >= maxAttempts {
		entry.BlockedUntil = now.Add(authRateCooldown)
		return false, authRateCooldown, true
	}
	entry.Attempts = append(entry.Attempts, now)
	entry.BlockedUntil = time.Time{}
	return true, 0, false
}

func enforceAuthRateLimit(w http.ResponseWriter, r *http.Request, scope string) bool {
	clientIP := getIP(r)
	allowed, retryAfter, newlyBlocked := checkAuthRateLimit(clientIP, scope, time.Now())
	if allowed {
		return true
	}
	retrySeconds := int(math.Ceil(retryAfter.Seconds()))
	if retrySeconds < 1 {
		retrySeconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
	if newlyBlocked {
		appendSecurityAudit(securityAuditEvent{
			Category: "security",
			Action:   "auth.rate_limited",
			Actor:    securityAuditActorForRequest(r),
			Target:   scope,
			Details:  map[string]string{"client_ip": clientIP, "retry_after_seconds": strconv.Itoa(retrySeconds)},
		})
	}
	writeJSON(w, http.StatusTooManyRequests, statusResponse{Status: "rate_limited", Message: "Too many authorization requests; try again shortly"})
	return false
}
