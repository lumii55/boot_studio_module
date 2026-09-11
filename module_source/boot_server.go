package main

import (
	"archive/zip"
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ModDir                 = "/data/adb/modules/boot_creator"
	maxRequestBytes        = 32 << 20
	maxBootAnimationBytes  = 25 << 20
	maxPreviewBytes        = 6 << 20
	maxZipUncompressedSize = 512 << 20
	apiVersion             = 1
	companionVersionCode   = 6
)

var (
	deviceModel       = "Unknown Device"
	deviceResolution  = "Unknown"
	stateMu           sync.RWMutex
	pairedIP          string
	pairedToken       string
	pendingAuth       *authRequest
	previewMu         sync.Mutex
	previewActive     bool
	pendingPair       *pairRegistration
	pairTokenPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{40,64}$`)
	historyIDPattern  = regexp.MustCompile(`^[0-9]{10,19}$`)
	resolutionPattern = regexp.MustCompile(`[0-9]+x[0-9]+`)
)

type authRequest struct {
	nonce  string
	result chan bool
}

type pairRegistration struct {
	token   string
	expires time.Time
}

type statusResponse struct {
	Status     string `json:"status"`
	Model      string `json:"model,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	HasCustom  bool   `json:"has_custom,omitempty"`
	Token      string `json:"token,omitempty"`
	Message    string `json:"message,omitempty"`
}

type infoResponse struct {
	API                  int      `json:"api_version"`
	ModuleVersion        string   `json:"module_version"`
	ModuleVersionCode    int      `json:"module_version_code"`
	CompanionVersionCode int      `json:"companion_version_code"`
	Features             []string `json:"features"`
}

func init() {
	out, err := exec.Command("/system/bin/getprop", "ro.product.model").Output()
	if err == nil {
		model := strings.TrimSpace(string(out))
		if model != "" {
			deviceModel = model
		}
	}

	resOut, resErr := exec.Command("/system/bin/wm", "size").Output()
	if resErr == nil {
		matches := resolutionPattern.FindAllString(string(resOut), -1)
		if len(matches) > 0 {
			deviceResolution = matches[len(matches)-1]
		}
	}
}

func readModuleMetadata() (string, int) {
	data, err := os.ReadFile(filepath.Join(ModDir, "module.prop"))
	if err != nil {
		return "unknown", 0
	}

	version := "unknown"
	versionCode := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "version":
			if v := strings.TrimSpace(value); v != "" {
				version = v
			}
		case "versionCode":
			if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				versionCode = v
			}
		}
	}
	return version, versionCode
}

func moduleFeatures() []string {
	return []string{
		"session_auth",
		"direct_upload",
		"pull",
		"history",
		"history_webm",
		"remove",
		"reset",
		"test_animation",
		"device_resolution",
		"update_preservation",
		"qr_pairing",
	}
}

func writeLog(message string) {
	logPath := ModDir + "/boot_creator.log"
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	_, _ = f.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
}

func showToast(msg string) {
	go func() {
		_ = exec.Command(
			"/system/bin/am",
			"broadcast",
			"-a", "com.bootcreator.SHOW_TOAST",
			"-n", "com.bootcreator.companion/.ToastReceiver",
			"-e", "msg", msg,
		).Run()
	}()
}

func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func isAllowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}

	if u.Scheme == "https" && u.Hostname() == "lumii55.github.io" {
		return true
	}

	if u.Scheme == "http" {
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
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

	origin := r.Header.Get("Origin")
	if origin != "" {
		if !isAllowedOrigin(origin) {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Boot-Creator-Token")
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowedMethods, ", ")+
			", OPTIONS")
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

func secureEqual(a, b string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func isAuthorized(r *http.Request) bool {
	clientIP := getIP(r)
	token := r.Header.Get("X-Boot-Creator-Token")

	stateMu.RLock()
	valid := pairedIP != "" && pairedIP == clientIP && secureEqual(pairedToken, token)
	stateMu.RUnlock()

	return valid
}

func requireAuthorization(w http.ResponseWriter, r *http.Request) bool {
	if isAuthorized(r) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, statusResponse{Status: "error", Message: "Access denied"})
	return false
}

func checkHasCustomAnim() bool {
	paths, err := readSavedPaths()
	if err != nil {
		return false
	}

	for _, targetPath := range paths {
		if info, err := os.Stat(ModDir + targetPath); err == nil && !info.IsDir() {
			return true
		}
	}

	return false
}

func readSavedPaths() ([]string, error) {
	data, err := os.ReadFile(ModDir + "/saved_paths.txt")
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		targetPath := strings.TrimSpace(raw)
		if targetPath == "" {
			continue
		}
		cleaned, ok := validateTargetPath(targetPath)
		if ok {
			paths = append(paths, cleaned)
		}
	}

	if len(paths) == 0 {
		return nil, errors.New("no valid paths")
	}

	return paths, nil
}

func validateTargetPath(targetPath string) (string, bool) {
	if !strings.HasPrefix(targetPath, "/") || strings.ContainsRune(targetPath, '\x00') {
		return "", false
	}

	cleaned := filepath.Clean(targetPath)
	if cleaned != targetPath || !strings.HasSuffix(cleaned, "/bootanimation.zip") {
		return "", false
	}

	return cleaned, true
}

func infoHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	version, versionCode := readModuleMetadata()
	writeJSON(w, http.StatusOK, infoResponse{
		API:                  apiVersion,
		ModuleVersion:        version,
		ModuleVersionCode:    versionCode,
		CompanionVersionCode: companionVersionCode,
		Features:             moduleFeatures(),
	})
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	if !isAuthorized(r) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "auth_required"})
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{
		Status:     "ok",
		Model:      deviceModel,
		Resolution: deviceResolution,
		HasCustom:  checkHasCustomAnim(),
	})
}

func requestAuthHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}

	clientIP := getIP(r)
	origin := r.Header.Get("Origin")
	nonce, err := randomToken(24)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create authorization request"})
		writeLog("Error: Failed to generate authorization nonce.")
		return
	}

	request := &authRequest{
		nonce:  nonce,
		result: make(chan bool, 1),
	}

	stateMu.Lock()
	if pendingAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
		return
	}
	pendingAuth = request
	stateMu.Unlock()

	defer func() {
		stateMu.Lock()
		if pendingAuth == request {
			pendingAuth = nil
		}
		stateMu.Unlock()
	}()

	originLabel := originDisplayLabel(origin)

	command := "am start -n com.bootcreator.companion/.PromptActivity --es nonce " + nonce + " --es requester_ip " + clientIP + " --es request_origin " + originLabel
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not open authorization prompt"})
		writeLog("Error: Failed to launch authorization prompt for IP: " + clientIP)
		return
	}

	select {
	case allowed := <-request.result:
		if !allowed {
			writeJSON(w, http.StatusForbidden, statusResponse{Status: "denied"})
			showToast("❌ Boot Creator: Connection denied by user.")
			writeLog("Connection denied by user from IP: " + clientIP)
			return
		}

		token, err := randomToken(32)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create session"})
			writeLog("Error: Failed to generate session token for IP: " + clientIP)
			return
		}

		stateMu.Lock()
		pairedIP = clientIP
		pairedToken = token
		stateMu.Unlock()

		writeJSON(w, http.StatusOK, statusResponse{
			Status:     "ok",
			Model:      deviceModel,
			Resolution: deviceResolution,
			HasCustom:  checkHasCustomAnim(),
			Token:      token,
		})
		showToast("✨ Boot Creator: Website connected successfully!")
		writeLog("Connection allowed by user from IP: " + clientIP)
	case <-time.After(30 * time.Second):
		writeJSON(w, http.StatusRequestTimeout, statusResponse{Status: "timeout"})
		writeLog("Connection prompt timed out for IP: " + clientIP)
	}
}

func authCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := net.ParseIP(getIP(r))
	if clientIP == nil || !clientIP.IsLoopback() {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	nonce := r.URL.Query().Get("nonce")
	allowValue := r.URL.Query().Get("allow")
	if nonce == "" || (allowValue != "true" && allowValue != "false") {
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}

	stateMu.RLock()
	request := pendingAuth
	valid := request != nil && secureEqual(request.nonce, nonce)
	stateMu.RUnlock()

	if !valid {
		http.Error(w, "Authorization request not found", http.StatusForbidden)
		return
	}

	select {
	case request.result <- allowValue == "true":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Authorization request already completed", http.StatusConflict)
	}
}

func validPairToken(token string) bool {
	return pairTokenPattern.MatchString(token)
}

func localIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				continue
			}
			ip := ipNet.IP.To4()
			if ip != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

func pairRegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := net.ParseIP(getIP(r))
	if clientIP == nil || !clientIP.IsLoopback() {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		http.Error(w, "Invalid pairing token", http.StatusBadRequest)
		return
	}

	stateMu.Lock()
	pendingPair = &pairRegistration{token: token, expires: time.Now().Add(2 * time.Minute)}
	stateMu.Unlock()

	writeJSON(w, http.StatusOK, statusResponse{Status: "ready"})
	phoneIP := localIPv4()
	if phoneIP != "" {
		showToast("👀 Boot Creator: QR approved. Return to the other device. Phone IP: " + phoneIP)
	} else {
		showToast("👀 Boot Creator: QR approved. Return to the other device to finish pairing.")
	}
	writeLog("QR pairing token registered locally.")
}

func pairStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		writeJSON(w, http.StatusOK, statusResponse{Status: "waiting"})
		return
	}

	stateMu.Lock()
	pair := pendingPair
	if pair != nil && time.Now().After(pair.expires) {
		pendingPair = nil
		pair = nil
	}
	ready := pair != nil && secureEqual(pair.token, token)
	stateMu.Unlock()

	if ready {
		writeJSON(w, http.StatusOK, statusResponse{Status: "ready"})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "waiting"})
}

func pairExchangeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}

	token := r.URL.Query().Get("token")
	if !validPairToken(token) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid pairing token"})
		return
	}

	stateMu.Lock()
	pair := pendingPair
	if pair != nil && time.Now().After(pair.expires) {
		pendingPair = nil
		pair = nil
	}
	if pair == nil || !secureEqual(pair.token, token) {
		stateMu.Unlock()
		writeJSON(w, http.StatusForbidden, statusResponse{Status: "error", Message: "Pairing token not registered"})
		return
	}
	if pendingAuth != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy"})
		return
	}

	nonce, err := randomToken(24)
	if err != nil {
		stateMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create pairing request"})
		return
	}

	request := &authRequest{nonce: nonce, result: make(chan bool, 1)}
	pendingAuth = request
	stateMu.Unlock()

	defer func() {
		stateMu.Lock()
		if pendingAuth == request {
			pendingAuth = nil
		}
		stateMu.Unlock()
	}()

	clientIP := getIP(r)
	originLabel := originDisplayLabel(r.Header.Get("Origin"))
	command := "am start -n com.bootcreator.companion/.PromptActivity --ez auto_pair true --es nonce " + nonce + " --es pair_token " + token + " --es requester_ip " + clientIP + " --es request_origin " + originLabel
	if err := exec.Command("su", "2000", "-c", command).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not verify pairing approval"})
		writeLog("Error: Failed to verify QR pairing for IP: " + clientIP)
		return
	}

	select {
	case allowed := <-request.result:
		if !allowed {
			writeJSON(w, http.StatusForbidden, statusResponse{Status: "denied"})
			writeLog("QR pairing verification failed for IP: " + clientIP)
			return
		}

		session, err := randomToken(32)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create session"})
			return
		}

		stateMu.Lock()
		pairedIP = clientIP
		pairedToken = session
		pendingPair = nil
		stateMu.Unlock()

		writeJSON(w, http.StatusOK, statusResponse{
			Status:     "ok",
			Model:      deviceModel,
			Resolution: deviceResolution,
			HasCustom:  checkHasCustomAnim(),
			Token:      session,
		})
		showToast("✨ Boot Creator: QR pairing completed successfully!")
		writeLog("QR pairing completed for IP: " + clientIP)
	case <-time.After(12 * time.Second):
		writeJSON(w, http.StatusRequestTimeout, statusResponse{Status: "timeout"})
		writeLog("QR pairing verification timed out for IP: " + clientIP)
	}
}

func disconnectHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	stateMu.Lock()
	pairedIP = ""
	pairedToken = ""
	stateMu.Unlock()

	writeJSON(w, http.StatusOK, statusResponse{Status: "disconnected"})
	showToast("🔌 Boot Creator: Website disconnected.")
	writeLog("Website disconnected manually by user.")
}

func cleanHistory(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var zips []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".zip") {
			zips = append(zips, entry.Name())
		}
	}

	if len(zips) <= 5 {
		return
	}

	sort.Strings(zips)
	for i := 0; i < len(zips)-5; i++ {
		base := strings.TrimSuffix(zips[i], ".zip")
		_ = os.Remove(filepath.Join(dir, base+".zip"))
		_ = os.Remove(filepath.Join(dir, base+".webm"))
		_ = os.Remove(filepath.Join(dir, base+".gif"))
	}
	showToast("🧹 Boot Creator: Automatic cleanup removed old animations from history.")
	writeLog("Automatic history cleanup performed. Oldest animations deleted.")
}

func newHistoryID(dir string) string {
	id := time.Now().UnixMilli()
	for {
		candidate := strconv.FormatInt(id, 10)
		if _, err := os.Stat(filepath.Join(dir, candidate+".zip")); os.IsNotExist(err) {
			return candidate
		}
		id++
	}
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func saveMultipartFile(file io.Reader, destination string, limit int64) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	limited := io.LimitReader(file, limit+1)
	written, copyErr := io.Copy(output, limited)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		_ = os.Remove(destination)
		return errors.New("file too large")
	}
	return nil
}

func validateBootAnimation(zipPath string) error {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return errors.New("invalid zip file")
	}
	defer archive.Close()

	if len(archive.File) == 0 || len(archive.File) > 10000 {
		return errors.New("invalid zip contents")
	}

	var descFile *zip.File
	hasImage := false
	var totalUncompressed uint64

	for _, entry := range archive.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") {
			return errors.New("unsafe zip path")
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return errors.New("unsafe zip path")
			}
		}

		totalUncompressed += entry.UncompressedSize64
		if totalUncompressed > maxZipUncompressedSize {
			return errors.New("zip expands beyond the allowed size")
		}

		if name == "desc.txt" {
			descFile = entry
		}

		lower := strings.ToLower(name)
		if strings.Contains(name, "/") && (strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg")) {
			hasImage = true
		}
	}

	if descFile == nil {
		return errors.New("desc.txt not found")
	}
	if !hasImage {
		return errors.New("no animation frames found")
	}
	if descFile.UncompressedSize64 > 64<<10 {
		return errors.New("desc.txt is too large")
	}

	descReader, err := descFile.Open()
	if err != nil {
		return errors.New("could not read desc.txt")
	}
	defer descReader.Close()

	scanner := bufio.NewScanner(descReader)
	if !scanner.Scan() {
		return errors.New("desc.txt is empty")
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 3 {
		return errors.New("invalid desc.txt header")
	}

	width, widthErr := strconv.Atoi(fields[0])
	height, heightErr := strconv.Atoi(fields[1])
	fps, fpsErr := strconv.Atoi(fields[2])
	if widthErr != nil || heightErr != nil || fpsErr != nil || width < 1 || height < 1 || fps < 1 || width > 16384 || height > 16384 || fps > 240 {
		return errors.New("invalid desc.txt dimensions or fps")
	}

	return nil
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, statusResponse{Status: "error", Message: "Upload is too large or invalid"})
		writeLog("Error: Upload rejected because the multipart request was too large or invalid.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, _, err := r.FormFile("bootanimation")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Failed to receive bootanimation.zip"})
		writeLog("Error: Failed to receive bootanimation.zip during upload.")
		return
	}
	defer file.Close()

	tempFile, err := os.CreateTemp("/data/local/tmp", "boot_creator_upload_*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create temporary file"})
		writeLog("Error: Failed to create temporary upload file.")
		return
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	if err := saveMultipartFile(file, tempPath, maxBootAnimationBytes); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, statusResponse{Status: "error", Message: "bootanimation.zip exceeds the allowed size"})
		writeLog("Error: bootanimation.zip exceeded the allowed upload size.")
		return
	}

	if err := validateBootAnimation(tempPath); err != nil {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: err.Error()})
		writeLog("Error: Invalid boot animation upload: " + err.Error())
		return
	}

	historyDir := ModDir + "/history"
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare history"})
		writeLog("Error: Failed to create history directory.")
		return
	}

	timestamp := newHistoryID(historyDir)
	historyZipPath := filepath.Join(historyDir, timestamp+".zip")
	if err := copyFile(tempPath, historyZipPath, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not save history item"})
		writeLog("Error: Failed to save boot animation to history.")
		return
	}

	previewPath := ""
	previewFile, _, previewErr := r.FormFile("preview")
	if previewErr == nil {
		defer previewFile.Close()
		previewPath = filepath.Join(historyDir, timestamp+".webm")
		if err := saveMultipartFile(previewFile, previewPath, maxPreviewBytes); err != nil {
			_ = os.Remove(previewPath)
			previewPath = ""
			writeLog("Warning: Preview file was rejected because it exceeded the allowed size.")
		}
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", tempPath)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(historyZipPath)
		if previewPath != "" {
			_ = os.Remove(previewPath)
		}
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Failed to inject boot animation"})
		showToast("❌ Boot Creator: An error occurred while injecting the ZIP.")
		writeLog("Error: Failed to inject new boot animation via inject.sh.")
		return
	}

	cleanHistory(historyDir)
	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToast("🚀 Boot Creator: New animation injected and ready for the next boot!")
	writeLog("Success: New boot animation injected and saved to history (ID: " + timestamp + ").")
}

func removeHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/clean.sh")
	if err := cmd.Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error running clean.sh"})
		writeLog("Error: Failed to run clean.sh during removal.")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Animation successfully removed"})
	showToast("🗑️ Boot Creator: Custom animation removed. Original boot restored!")
	writeLog("Success: Custom boot animation removed. Restored to stock.")
}

func pullHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	source := r.URL.Query().Get("source")
	if source != "module" && source != "system" {
		http.Error(w, "Invalid source", http.StatusBadRequest)
		return
	}

	paths, err := readSavedPaths()
	if err != nil {
		http.Error(w, "No valid paths found", http.StatusNotFound)
		writeLog("Error: Attempted to pull animation, but no valid saved paths were found.")
		return
	}

	targetPath := paths[0]
	fileToServe := targetPath

	if source == "module" {
		fileToServe = ModDir + targetPath
		showToast("📥 Boot Creator: The website extracted your custom animation from the module!")
		writeLog("Animation pulled by website (Source: Module).")
	} else {
		backupPath := ModDir + "/backup" + targetPath
		if _, err := os.Stat(backupPath); err == nil {
			fileToServe = backupPath
		}
		showToast("📥 Boot Creator: The website extracted your stock animation!")
		writeLog("Animation pulled by website (Source: Stock System).")
	}

	info, err := os.Stat(fileToServe)
	if err != nil || info.IsDir() {
		http.Error(w, "File not found", http.StatusNotFound)
		writeLog("Error: Pulled animation file not found at " + fileToServe)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=pulled_bootanimation.zip")
	w.Header().Set("Content-Type", "application/zip")
	http.ServeFile(w, r, fileToServe)
}

func resetHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	files := []string{
		ModDir + "/saved_paths.txt",
		ModDir + "/system.prop",
		ModDir + "/boot_creator.log",
		ModDir + "/boot_creator.previous.log",
	}
	dirs := []string{
		ModDir + "/system",
		ModDir + "/product",
		ModDir + "/oem",
		ModDir + "/vendor",
		ModDir + "/system_ext",
		ModDir + "/apex",
		ModDir + "/custom",
		ModDir + "/history",
	}

	for _, filePath := range files {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error resetting the module"})
			writeLog("Error: Failed to reset module file: " + filePath)
			return
		}
	}

	for _, dirPath := range dirs {
		if err := os.RemoveAll(dirPath); err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error resetting the module"})
			writeLog("Error: Failed to reset module directory: " + dirPath)
			return
		}
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Module reset successfully"})
	showToast("⚠️ Boot Creator: Module has been reset for troubleshooting.")
	writeLog("Success: Module troubleshooting reset executed. History cleared.")
}

func validHistoryID(id string) bool {
	return historyIDPattern.MatchString(id)
}

func historyListHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	historyDir := ModDir + "/history"
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not read history"})
		return
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".zip")
		if validHistoryID(id) {
			ids = append(ids, id)
		}
	}

	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	writeJSON(w, http.StatusOK, ids)
}

func historyPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		http.Error(w, "Invalid history ID", http.StatusBadRequest)
		return
	}

	previewPath := filepath.Join(ModDir, "history", id+".webm")
	info, err := os.Stat(previewPath)
	if err != nil || info.IsDir() {
		previewPath = filepath.Join(ModDir, "history", id+".gif")
		info, err = os.Stat(previewPath)
	}
	if err != nil || info.IsDir() {
		http.Error(w, "Preview not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", "video/webm")
	http.ServeFile(w, r, previewPath)
}

func historyApplyHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid history ID"})
		return
	}

	historyPath := filepath.Join(ModDir, "history", id+".zip")
	if info, err := os.Stat(historyPath); err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "History item not found"})
		return
	}

	tempFile, err := os.CreateTemp("/data/local/tmp", "boot_creator_history_*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not create temporary file"})
		return
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	if err := copyFile(historyPath, tempPath, 0600); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not prepare history item"})
		writeLog("Error: Failed to prepare history item for restore (ID: " + id + ").")
		return
	}

	cmd := exec.Command("/system/bin/sh", ModDir+"/inject.sh", tempPath)
	if err := cmd.Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Error injecting history"})
		writeLog("Error: Failed to restore animation from history (ID: " + id + ").")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Animation restored from history"})
	showToast("⏳ Boot Creator: Past animation restored successfully!")
	writeLog("Success: Animation restored from history (ID: " + id + ").")
}

func historyDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}

	id := r.URL.Query().Get("id")
	if !validHistoryID(id) {
		writeJSON(w, http.StatusBadRequest, statusResponse{Status: "error", Message: "Invalid history ID"})
		return
	}

	zipPath := filepath.Join(ModDir, "history", id+".zip")
	previewWebMPath := filepath.Join(ModDir, "history", id+".webm")
	previewLegacyPath := filepath.Join(ModDir, "history", id+".gif")
	removed := false

	if err := os.Remove(zipPath); err == nil {
		removed = true
	} else if !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not delete history item"})
		return
	}

	for _, previewPath := range []string{previewWebMPath, previewLegacyPath} {
		if err := os.Remove(previewPath); err != nil && !os.IsNotExist(err) {
			writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not delete history preview"})
			return
		}
	}

	if !removed {
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "History item not found"})
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "success"})
	showToast("🧹 Boot Creator: An old animation was deleted from history.")
	writeLog("History item deleted manually by user (ID: " + id + ").")
}

func acquirePreview() bool {
	previewMu.Lock()
	defer previewMu.Unlock()
	if previewActive {
		return false
	}
	previewActive = true
	return true
}

func releasePreview() {
	previewMu.Lock()
	previewActive = false
	previewMu.Unlock()
}

func testAnimHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodPost) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	if !acquirePreview() {
		writeJSON(w, http.StatusConflict, statusResponse{Status: "busy", Message: "A preview is already running"})
		return
	}

	paths, err := readSavedPaths()
	if err != nil {
		releasePreview()
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "No valid saved path found"})
		return
	}

	targetPath := paths[0]
	modulePath := ModDir + targetPath
	if info, err := os.Stat(modulePath); err != nil || info.IsDir() {
		releasePreview()
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "Custom animation not found in module"})
		return
	}
	if info, err := os.Stat(targetPath); err != nil || info.IsDir() {
		releasePreview()
		writeJSON(w, http.StatusNotFound, statusResponse{Status: "error", Message: "System animation path not found"})
		return
	}

	_ = exec.Command("/system/bin/umount", targetPath).Run()
	if err := exec.Command("/system/bin/mount", "-o", "bind", modulePath, targetPath).Run(); err != nil {
		releasePreview()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not mount preview animation"})
		writeLog("Error: Failed to bind mount preview animation.")
		return
	}
	if err := exec.Command("/system/bin/setprop", "ctl.start", "bootanim").Run(); err != nil {
		_ = exec.Command("/system/bin/umount", targetPath).Run()
		releasePreview()
		writeJSON(w, http.StatusInternalServerError, statusResponse{Status: "error", Message: "Could not start boot animation preview"})
		writeLog("Error: Failed to start boot animation preview.")
		return
	}

	showToast("👀 Boot Creator: Showing device preview. Look at your screen!")
	writeLog("Preview triggered on device using bind mount.")

	go func() {
		defer releasePreview()
		time.Sleep(15 * time.Second)
		_ = exec.Command("/system/bin/setprop", "ctl.stop", "bootanim").Run()
		time.Sleep(1 * time.Second)
		_ = exec.Command("/system/bin/umount", targetPath).Run()
		showToast("🏁 Boot Creator: Preview finished!")
		writeLog("Preview finished and unmounted.")
	}()

	writeJSON(w, http.StatusOK, statusResponse{Status: "success", Message: "Preview started"})
}

func main() {
	writeLog("=== Boot Creator Server Started ===")

	http.HandleFunc("/info", infoHandler)
	http.HandleFunc("/ping", pingHandler)
	http.HandleFunc("/request_auth", requestAuthHandler)
	http.HandleFunc("/auth_callback", authCallbackHandler)
	http.HandleFunc("/pair_register", pairRegisterHandler)
	http.HandleFunc("/pair_status", pairStatusHandler)
	http.HandleFunc("/pair_exchange", pairExchangeHandler)
	http.HandleFunc("/disconnect", disconnectHandler)
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/remove", removeHandler)
	http.HandleFunc("/pull", pullHandler)
	http.HandleFunc("/reset", resetHandler)
	http.HandleFunc("/history/list", historyListHandler)
	http.HandleFunc("/history/preview", historyPreviewHandler)
	http.HandleFunc("/history/apply", historyApplyHandler)
	http.HandleFunc("/history/delete", historyDeleteHandler)
	http.HandleFunc("/test_anim", testAnimHandler)

	server := &http.Server{
		Addr:              "0.0.0.0:4040",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       90 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	fmt.Println("✨ Boot Creator server running on port 4040...")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		writeLog("Server stopped with error: " + err.Error())
	}
}
