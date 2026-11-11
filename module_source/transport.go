package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func currentTransportFingerprint() string {
	if stored, ok := transportFingerprint.Load().(string); ok {
		return stored
	}
	return ""
}

// P14.12: TLS is the ONLY network-facing BAS transport on port 4041.
// Port 4040 remains strictly loopback-only for the Android Companion APK's
// auth/pair callbacks and the on-device WebUI. It must never be a remote
// fallback. A remote browser must trust and verify this self-signed certificate
// against the fingerprint displayed locally on the Android device.
func transportIPAddresses(ips []net.IP) []net.IP {
	seen := make(map[string]bool)
	result := make([]net.IP, 0, len(ips)+2)
	for _, ip := range append([]net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}, ips...) {
		// Only addresses used by BAS browser clients belong in the leaf SANs.
		// Excluding rotating IPv6 privacy/cellular addresses prevents a needless
		// certificate identity change on practically every Android reboot.
		if ip == nil || ip.IsUnspecified() || (!ip.IsLoopback() && (!ip.IsPrivate() || ip.To4() == nil)) {
			continue
		}
		canonical := ip.To16()
		if canonical == nil {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			canonical = ip4
		}
		key := canonical.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, canonical)
	}
	return result
}

func transportFingerprintOf(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	digest := sha256.Sum256(cert.Raw)
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

func loadTransportCertificate(certPath, keyPath string, ips []net.IP, now time.Time) (tls.Certificate, string, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return tls.Certificate{}, "", errors.New("transport certificate expired")
	}
	if err := cert.VerifyHostname("localhost"); err != nil {
		return tls.Certificate{}, "", err
	}
	for _, ip := range ips {
		if err := cert.VerifyHostname(ip.String()); err != nil {
			return tls.Certificate{}, "", err
		}
	}
	return pair, transportFingerprintOf(cert), nil
}

func writePrivateTransportFile(path string, content []byte) error {
	// Directory is root-owned and mode 0700. Write to a fresh temporary file,
	// then rename atomically; no insecure intermediate private key on disk.
	temp, err := os.CreateTemp(filepath.Dir(path), ".transport-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func ensureTransportCertificate(directory string, rawIPs []net.IP, now time.Time) (tls.Certificate, string, error) {
	ips := transportIPAddresses(rawIPs)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return tls.Certificate{}, "", err
	}
	certPath, keyPath := filepath.Join(directory, "server-cert.pem"), filepath.Join(directory, "server-key.pem")
	if pair, fp, err := loadTransportCertificate(certPath, keyPath, ips, now); err == nil {
		return pair, fp, nil
	}

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "Boot Animation Studio Companion (device-local)"},
		NotBefore: now.Add(-1 * time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: ips,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyBytes, err := x509.MarshalECPrivateKey(private)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writePrivateTransportFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writePrivateTransportFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return tls.Certificate{}, "", err
	}
	return loadTransportCertificate(certPath, keyPath, ips, now)
}

func transportIdentityHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !isLocalDeviceRequest(r) {
		http.Error(w, "Local device only", http.StatusForbidden)
		return
	}
	if currentTransportFingerprint() == "" {
		writeJSON(w, http.StatusServiceUnavailable, statusResponse{Status: "error", Message: "Remote HTTPS is unavailable; local Companion access remains available"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "protocol": "https", "port": 4041, "fingerprint_sha256": currentTransportFingerprint(), "manual_verification_required": true})
}

func startSecureTransportServer(handler http.Handler, directory string, ips []net.IP, now time.Time) (string, error) {
	cert, fingerprint, err := ensureTransportCertificate(directory, ips, now)
	if err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", "0.0.0.0:4041")
	if err != nil {
		return "", err
	}
	var certificateMu sync.Mutex
	current := cert
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Minute, WriteTimeout: 30 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	server.TLSConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		certificateMu.Lock()
		defer certificateMu.Unlock()
		// Android can acquire its Wi-Fi IP *after* the boot service starts.
		// Renew only when the certificate lacks the actual listener IP or has
		// expired; do not mutate the identity on every accepted handshake.
		localIPs := transportIPAddresses(localDeviceIPs())
		if hello != nil && hello.Conn != nil {
			if address, ok := hello.Conn.LocalAddr().(*net.TCPAddr); ok && address.IP != nil {
				localIPs = transportIPAddresses(append(localIPs, address.IP))
			}
		}
		leaf, parseErr := x509.ParseCertificate(current.Certificate[0])
		renew := parseErr != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter)
		if !renew {
			for _, ip := range localIPs {
				if leaf.VerifyHostname(ip.String()) != nil {
					renew = true
					break
				}
			}
		}
		if renew {
			updated, fp, err := ensureTransportCertificate(directory, localIPs, time.Now())
			if err != nil {
				return nil, err
			}
			current = updated
			transportFingerprint.Store(fp)
			writeLog("Companion HTTPS certificate renewed after a local IP/validity change; browsers must re-verify its fingerprint.")
		}
		return &current, nil
	}
	// Publish readiness before the serving goroutine begins; clear it if the
	// listener later stops, so the local diagnostic UI cannot advertise stale TLS.
	transportFingerprint.Store(fingerprint)
	go func() {
		if err := server.ServeTLS(listener, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			writeLog("HTTPS companion listener stopped: " + err.Error())
		}
		transportFingerprint.Store("")
	}()
	return fingerprint, nil
}
