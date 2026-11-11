package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func decodeJSONRequest(r *http.Request, limit int64, target interface{}) error {
	if r.Body == nil {
		return errors.New("empty request body")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("request body too large")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty request body")
	}
	return json.Unmarshal(data, target)
}

func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func localDeviceIPs() []net.IP {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	ips := make([]net.IP, 0, 8)
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			var ip net.IP
			switch value := address.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if ip != nil {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

func isLocalDeviceIPString(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, localIP := range localDeviceIPs() {
		if localIP.Equal(ip) {
			return true
		}
	}
	return false
}

func isLocalDeviceRequest(r *http.Request) bool {
	return isLocalDeviceIPString(getIP(r))
}

func isAllowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}

	if u.Scheme == "https" && u.Host == "lumii55.github.io" {
		return true
	}

	if u.Scheme == "http" || u.Scheme == "https" {
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return true
		}
		if isModuleWebUIOrigin(u) {
			return true
		}
	}

	return false
}

func originDisplayLabel(origin string) string {
	if origin == "https://lumii55.github.io" {
		return origin
	}

	u, err := url.Parse(origin)
	if err == nil && u.Scheme == "http" {
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return u.Scheme + "://" + host
		}
	}

	return "Local-client"
}

func prepareRequest(w http.ResponseWriter, r *http.Request, allowedMethods ...string) bool {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	origin := r.Header.Get("Origin")
	if origin != "" {
		if !isAllowedOrigin(origin) {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Boot-Creator-Token, X-Boot-Creator-Client-ID, X-Boot-Creator-Client-Name")
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowedMethods, ", ")+
			", OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "600")
		// Lets the Studio read the rate-limit wait time and tell the user how long to wait.
		w.Header().Set("Access-Control-Expose-Headers", "Retry-After")
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
	}

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}

	for _, method := range allowedMethods {
		if r.Method == method {
			return true
		}
	}

	w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return false
}

func getIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
