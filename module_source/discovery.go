package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// LAN discovery signal (v1.5).
//
// Since P14.12 the Companion API is not reachable over plain HTTP from the network, and an
// HTTPS port with a not-yet-trusted certificate looks like a closed port to a browser. So the
// Studio's network scan could no longer find v1.5 phones. This listener restores discovery
// and nothing else:
//
//   - its own port (4042) and its own handler; it never references newCompanionMux, so no
//     Companion route, session, token or state can be reached through it;
//   - one fixed answer, GET /discover -> {"service","api_version","secure_port"}: no model,
//     bridge id, module version, features or any device data;
//   - no request body or query is read; tight timeouts; MaxHeaderBytes 2 KiB (with Go's
//     own read slack, requests with >= 16 KiB of headers are refused);
//   - browsers only get CORS access from allowed Studio origins; other websites are refused;
//   - owners can turn it off by creating ModDir/discovery.disabled.
//
// The actual connection still goes through HTTPS :4041 and the normal authorization.

const discoveryAddress = "0.0.0.0:4042"

var discoveryBody = []byte(`{"service":"bas-companion","api_version":` + strconv.Itoa(apiVersion) + `,"secure_port":4041}`)

func discoveryDisabled() bool { return discoveryDisabledIn(ModDir) }

func discoveryDisabledIn(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "discovery.disabled"))
	return err == nil
}

func discoveryHandler(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != "/discover" {
		http.NotFound(w, r)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if !isAllowedOrigin(origin) {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		h.Set("Access-Control-Max-Age", "600")
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			h.Set("Access-Control-Allow-Private-Network", "true")
		}
	}
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet, http.MethodHead:
		h.Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(discoveryBody)
		}
	default:
		h.Set("Allow", "GET, OPTIONS")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func newDiscoveryServer() *http.Server {
	return &http.Server{
		Addr:              discoveryAddress,
		Handler:           http.HandlerFunc(discoveryHandler),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       10 * time.Second,
		MaxHeaderBytes:    2 << 10,
	}
}

// startDiscoveryServer runs the signal in the background. A failure only disables discovery;
// it never affects the Companion listeners.
func startDiscoveryServer() {
	if discoveryDisabled() {
		writeLog("LAN discovery signal on 4042 disabled by discovery.disabled")
		return
	}
	server := newDiscoveryServer()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			writeLog("LAN discovery signal on 4042 unavailable: " + err.Error())
		}
	}()
	writeLog("LAN discovery signal on 4042 ready (discovery only; connections use HTTPS 4041)")
}
