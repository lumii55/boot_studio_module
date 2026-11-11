package main

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ModDir                          = "/data/adb/modules/boot_creator"
	maxBootAnimationBytes           = int64(2) << 30
	directUploadWarningBytes        = int64(25) << 20
	maxPreviewBytes                 = int64(6) << 20
	maxRequestBytes                 = maxBootAnimationBytes + maxPreviewBytes + (16 << 20)
	maxZipUncompressedSize   uint64 = 8 << 30
	maxSavedPaths                   = 64
	historyLimit                    = 5
	historyMetadataVersion          = 1
	playlistStateVersion            = 1
	rotationStateVersion            = 3
	bootActivityStateVersion        = 1
	bootActivityLimit               = 50
	apiVersion                      = 1
	companionVersionCode            = 7
	webUISessionCookieName          = "boot_creator_webui"
	webUISessionTTL                 = 8 * time.Hour
	webUILogTailBytes        int64  = 512 << 10
	bridgeIdentityFile              = "bridge_id"
	trustStateVersion               = 2
	trustClientLimit                = 24
	trustChallengeTTL               = 90 * time.Second
	trustChallengeLimit             = 64
	securityAuditVersion            = 1
	securityAuditLimit              = 200
	operationLeaseTTL               = 30 * time.Minute
	authRateWindow                  = time.Minute
	authRateCooldown                = time.Minute
	authRateMaxAttempts             = 3
	authRateLocalMaxAttempts        = 6
	authRateRetention               = 10 * time.Minute
)

type persistentVersionDisposition uint8

const (
	persistentVersionUnsupported persistentVersionDisposition = iota
	persistentVersionMigration
	persistentVersionCurrent
	persistentVersionFuture
)

type persistentVersionPolicy struct {
	Domain         string
	MinimumVersion int
	CurrentVersion int
}

func classifyPersistentStateVersion(version int, policy persistentVersionPolicy) persistentVersionDisposition {
	if version < policy.MinimumVersion {
		return persistentVersionUnsupported
	}
	if version > policy.CurrentVersion {
		return persistentVersionFuture
	}
	if version == policy.CurrentVersion {
		return persistentVersionCurrent
	}
	return persistentVersionMigration
}

var (
	deviceModel             = "Unknown Device"
	deviceResolution        = "Unknown"
	bridgeID                string
	transportFingerprint    atomic.Value
	stateMu                 sync.RWMutex
	pairedIP                string
	pairedToken             string
	pairedCreatedAt         time.Time
	pairedLastSeen          time.Time
	pendingAuth             *authRequest
	pendingAsyncAuth        *asyncAuthRequest
	previewMu               sync.Mutex
	previewActive           bool
	previewTargetPaths      []string
	previewCancel           chan struct{}
	historyPreviewMu        sync.Mutex
	playlistMu              sync.Mutex
	playlistPreviewMu       sync.Mutex
	rotationMu              sync.Mutex
	bootActivityMu          sync.Mutex
	pendingPair             *pairRegistration
	webUISessionMu          sync.Mutex
	webUISessions           = make(map[string]time.Time)
	webUISecureSessions     = make(map[string]bool)
	trustMu                 sync.Mutex
	trustedSessionMu        sync.RWMutex
	trustedSessions         = make(map[string]*trustedSession)
	trustChallengeMu        sync.Mutex
	trustChallenges         = make(map[string]*trustChallenge)
	securityAuditMu         sync.Mutex
	liveMu                  sync.RWMutex
	liveEpoch               string
	liveRevisions           = make(map[string]uint64)
	liveSubscribers         = make(map[chan liveEvent]struct{})
	presenceMu              sync.Mutex
	presenceClients         = make(map[string]*presenceRecord)
	operationMu             sync.Mutex
	activeOperation         *operationLease
	authRateMu              sync.Mutex
	authRateByIP            = make(map[string]*authRateEntry)
	pairTokenPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{40,64}$`)
	historyIDPattern        = regexp.MustCompile(`^[0-9]{10,19}$`)
	playlistIDPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	playlistObjectPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	resolutionPattern       = regexp.MustCompile(`[0-9]+x[0-9]+`)
	bridgeIDPattern         = regexp.MustCompile(`^bc_[A-Za-z0-9_-]{20,64}$`)
	clientIDPattern         = regexp.MustCompile(`^bc_client_[A-Za-z0-9_-]{24,48}$`)
	trustedSessionIDPattern = regexp.MustCompile(`^bas_session_[A-Za-z0-9_-]{16,64}$`)
)

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
	features := []string{
		"session_auth",
		"async_auth",
		"direct_upload",
		"pull",
		"history",
		"history_webm",
		"history_details",
		"history_download",
		"history_generated_preview",
		"large_upload",
		"remove",
		"reset",
		"rescan_paths",
		"factory_reset",
		"test_animation",
		"test_staging",
		"playlists",
		"playlist_test",
		"boot_rotation",
		"boot_queue",
		"boot_activity",
		"device_resolution",
		"device_intelligence",
		"module_webui",
		"multi_device_identity",
		"trusted_clients",
		"trust_persistence_choice",
		"trust_permissions",
		"trust_permission_choice",
		"trust_session_management",
		"security_audit",
		"module_health",
		"module_health_maintenance",
		"disconnect_feedback",
		"live_events",
		"state_revisions",
		"client_presence",
		"operation_coordination",
		"multi_client_sessions",
		"update_preservation",
		"path_environment_status",
	}
	if currentTransportFingerprint() != "" {
		features = append(features, "https_transport")
	}
	return features
}

func main() {
	bridgeID = loadOrCreateBridgeID()
	initLiveSync()
	if len(os.Args) > 1 && os.Args[1] == "--prepare-rotation-after-boot" {
		if err := prepareRotationAfterBoot(); err != nil {
			writeLog("Rotation post-boot preparation failed: " + err.Error())
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}
	cleanupOrphanPreviewMounts()
	cleanupPreviewShadows()
	_ = os.RemoveAll(filepath.Join(ModDir, "staging"))
	writeLog("=== Boot Creator Server Started ===")
	writeLog("Mount namespace: server=" + mountNamespaceID("/proc/self/ns/mnt") + " init=" + mountNamespaceID("/proc/1/ns/mnt"))

	mux := newCompanionMux()
	// Network clients use TLS only. Failure to start TLS must never open a
	// network-facing HTTP fallback. The independent loopback listener remains
	// available for Android authorization/pair callbacks and local diagnostics.
	if fp, err := startSecureTransportServer(mux, filepath.Join(ModDir, "trust", "transport"), localDeviceIPs(), time.Now()); err != nil {
		writeLog("Remote HTTPS on 4041 UNAVAILABLE (no remote HTTP fallback): " + err.Error())
	} else {
		writeLog("Remote HTTPS on 4041 ready (verify certificate fingerprint locally): " + fp[:12] + "…")
	}

	startDiscoveryServer()

	server := newLoopbackCompanionServer(mux)
	fmt.Println("Boot Creator: loopback HTTP 127.0.0.1:4040; remote HTTPS 4041 only; LAN discovery signal 4042")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		writeLog("Local Companion HTTP listener stopped with error: " + err.Error())
	}
}
