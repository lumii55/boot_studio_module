package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type deviceProbeTarget struct {
	Path               string `json:"path"`
	GlobalPresent      bool   `json:"global_present"`
	ModuleOverlay      bool   `json:"module_overlay"`
	StockBackup        bool   `json:"stock_backup"`
	VerifiedSystemPath bool   `json:"verified_system_path"`
}

type bootAudioProbe struct {
	State         string   `json:"state"`
	Evidence      []string `json:"evidence,omitempty"`
	PlaySoundProp string   `json:"play_sound_property,omitempty"`
	SetVolumeProp string   `json:"set_volume_property,omitempty"`
}

type bootRendererProbe struct {
	Path            string `json:"path,omitempty"`
	Present         bool   `json:"present"`
	AudioWavMarker  bool   `json:"audio_wav_marker"`
	AudioConfMarker bool   `json:"audio_conf_marker"`
}

type deviceSystemProbe struct {
	Manufacturer  string `json:"manufacturer,omitempty"`
	Brand         string `json:"brand,omitempty"`
	Model         string `json:"model,omitempty"`
	Device        string `json:"device,omitempty"`
	Product       string `json:"product,omitempty"`
	Android       string `json:"android,omitempty"`
	SDK           int    `json:"sdk,omitempty"`
	BuildID       string `json:"build_id,omitempty"`
	BuildDisplay  string `json:"build_display,omitempty"`
	SecurityPatch string `json:"security_patch,omitempty"`
	Slot          string `json:"slot,omitempty"`
	Resolution    string `json:"resolution,omitempty"`
	DensityDPI    int    `json:"density_dpi,omitempty"`
}

type deviceBootProbe struct {
	PrimaryPath           string              `json:"primary_path,omitempty"`
	PrimaryPathConfidence string              `json:"primary_path_confidence"`
	CustomApplied         bool                `json:"custom_applied"`
	Targets               []deviceProbeTarget `json:"targets"`
	StockArchive          bootArchiveProbe    `json:"stock_archive"`
	CurrentArchive        bootArchiveProbe    `json:"current_archive"`
	Renderer              bootRendererProbe   `json:"renderer"`
	Audio                 bootAudioProbe      `json:"audio"`
	ServerMountNamespace  string              `json:"server_mount_namespace,omitempty"`
	InitMountNamespace    string              `json:"init_mount_namespace,omitempty"`
	GlobalMountAccess     bool                `json:"global_mount_access"`
	PathSchema            string              `json:"path_schema,omitempty"`
	PathEnvironmentStatus string              `json:"path_environment_status,omitempty"`
	RescanRecommended     bool                `json:"rescan_recommended"`
}

type deviceIntelligenceResponse struct {
	Status        string            `json:"status"`
	SchemaVersion int               `json:"schema_version"`
	CollectedAt   int64             `json:"collected_at"`
	System        deviceSystemProbe `json:"system"`
	Boot          deviceBootProbe   `json:"boot"`
}

func getPropValue(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	output, err := exec.Command("/system/bin/getprop", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 {
		return 0
	}
	return parsed
}

func readWMDensity() int {
	output, err := exec.Command("/system/bin/wm", "density").Output()
	if err != nil {
		return 0
	}
	matches := regexp.MustCompile(`[0-9]+`).FindAllString(string(output), -1)
	if len(matches) == 0 {
		return 0
	}
	return parsePositiveInt(matches[len(matches)-1])
}

func globalMountAccessAvailable() bool {
	selfNamespace := mountNamespaceID("/proc/self/ns/mnt")
	initNamespace := mountNamespaceID("/proc/1/ns/mnt")
	if selfNamespace != "unknown" && selfNamespace == initNamespace {
		return true
	}
	for _, path := range []string{"/system/bin/nsenter", "/system/bin/toybox"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true
		}
	}
	if _, err := exec.LookPath("nsenter"); err == nil {
		return true
	}
	return false
}

func fileContainsMarkers(path string, markers []string) (map[string]bool, error) {
	result := make(map[string]bool, len(markers))
	if len(markers) == 0 {
		return result, nil
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return result, errors.New("file unavailable")
	}
	const maxProbeBytes int64 = 64 << 20
	if info.Size() <= 0 || info.Size() > maxProbeBytes {
		return result, errors.New("file outside probe size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()

	maxMarker := 1
	for _, marker := range markers {
		if len(marker) > maxMarker {
			maxMarker = len(marker)
		}
	}
	buffer := make([]byte, 64<<10)
	overlap := make([]byte, 0, maxMarker-1)
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			chunk := make([]byte, 0, len(overlap)+n)
			chunk = append(chunk, overlap...)
			chunk = append(chunk, buffer[:n]...)
			for _, marker := range markers {
				if !result[marker] && bytes.Contains(chunk, []byte(marker)) {
					result[marker] = true
				}
			}
			if len(chunk) >= maxMarker-1 && maxMarker > 1 {
				overlap = append(overlap[:0], chunk[len(chunk)-(maxMarker-1):]...)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, readErr
		}
	}
	return result, nil
}

func findBootAnimationRenderer() bootRendererProbe {
	candidates := []string{
		"/system/bin/bootanimation",
		"/apex/com.android.bootanimation/bin/bootanimation",
		"/product/bin/bootanimation",
		"/system_ext/bin/bootanimation",
		"/vendor/bin/bootanimation",
		"/odm/bin/bootanimation",
	}
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		probe := bootRendererProbe{Path: path, Present: true}
		markers, err := fileContainsMarkers(path, []string{"audio.wav", "audio_conf.txt"})
		if err == nil {
			probe.AudioWavMarker = markers["audio.wav"]
			probe.AudioConfMarker = markers["audio_conf.txt"]
		}
		return probe
	}
	return bootRendererProbe{}
}

func collectDeviceSystemProbe() deviceSystemProbe {
	slot := strings.TrimSpace(getPropValue("ro.boot.slot_suffix"))
	if slot == "" {
		slot = strings.TrimSpace(getPropValue("ro.boot.slot"))
	}
	slot = strings.TrimPrefix(slot, "_")
	return deviceSystemProbe{
		Manufacturer:  getPropValue("ro.product.manufacturer"),
		Brand:         getPropValue("ro.product.brand"),
		Model:         getPropValue("ro.product.model"),
		Device:        getPropValue("ro.product.device"),
		Product:       getPropValue("ro.product.name"),
		Android:       getPropValue("ro.build.version.release"),
		SDK:           parsePositiveInt(getPropValue("ro.build.version.sdk")),
		BuildID:       getPropValue("ro.build.id"),
		BuildDisplay:  getPropValue("ro.build.display.id"),
		SecurityPatch: getPropValue("ro.build.version.security_patch"),
		Slot:          slot,
		Resolution:    deviceResolution,
		DensityDPI:    readWMDensity(),
	}
}

func collectDeviceBootProbe() deviceBootProbe {
	pathEnvStatus := pathEnvironmentStatus()
	probe := deviceBootProbe{
		PrimaryPathConfidence: "unknown",
		PathEnvironmentStatus: pathEnvStatus,
		RescanRecommended:     pathEnvStatus == "changed",
		CustomApplied:         checkHasCustomAnim(),
		Targets:               []deviceProbeTarget{},
		StockArchive:          bootArchiveProbe{Status: "missing", Format: "unknown"},
		CurrentArchive:        bootArchiveProbe{Status: "missing", Format: "unknown"},
		ServerMountNamespace:  mountNamespaceID("/proc/self/ns/mnt"),
		InitMountNamespace:    mountNamespaceID("/proc/1/ns/mnt"),
		GlobalMountAccess:     globalMountAccessAvailable(),
	}
	if schemaData, err := os.ReadFile(filepath.Join(ModDir, ".paths_schema")); err == nil {
		probe.PathSchema = strings.TrimSpace(string(schemaData))
	}

	paths, err := readSavedPaths()
	if err == nil {
		for _, targetPath := range paths {
			_, overlayErr := os.Stat(ModDir + targetPath)
			_, backupErr := os.Stat(filepath.Join(ModDir, "backup", targetPath))
			globalPresent := globalRegularFileExists(targetPath)
			backupPresent := backupErr == nil
			probe.Targets = append(probe.Targets, deviceProbeTarget{
				Path:               targetPath,
				GlobalPresent:      globalPresent,
				ModuleOverlay:      overlayErr == nil,
				StockBackup:        backupPresent,
				VerifiedSystemPath: backupPresent,
			})
		}
		if len(paths) > 0 {
			probe.PrimaryPath = paths[0]
			primary := probe.Targets[0]
			switch {
			case primary.VerifiedSystemPath:
				probe.PrimaryPathConfidence = "confirmed"
			case primary.GlobalPresent:
				probe.PrimaryPathConfidence = "observed"
			default:
				probe.PrimaryPathConfidence = "fallback"
			}
		}
	}

	stockPath := ""
	currentPath := ""
	if probe.PrimaryPath != "" {
		backupPath := filepath.Join(ModDir, "backup", probe.PrimaryPath)
		if info, err := os.Stat(backupPath); err == nil && !info.IsDir() {
			stockPath = backupPath
		} else if !probe.CustomApplied {
			if info, err := os.Stat(probe.PrimaryPath); err == nil && !info.IsDir() {
				stockPath = probe.PrimaryPath
			}
		}
		overlayPath := ModDir + probe.PrimaryPath
		if info, err := os.Stat(overlayPath); err == nil && !info.IsDir() {
			currentPath = overlayPath
		} else if stockPath != "" {
			currentPath = stockPath
		}
	}
	if stockPath != "" {
		probe.StockArchive = inspectBootArchiveProbe(stockPath)
	}
	if currentPath != "" {
		probe.CurrentArchive = inspectBootArchiveProbe(currentPath)
	}

	probe.Renderer = findBootAnimationRenderer()
	audio := bootAudioProbe{
		State:         "unknown",
		Evidence:      []string{},
		PlaySoundProp: getPropValue("persist.sys.bootanim.play_sound"),
		SetVolumeProp: getPropValue("ro.bootanim.set_volume"),
	}
	if probe.Renderer.AudioWavMarker {
		audio.State = "observed"
		audio.Evidence = append(audio.Evidence, "renderer_audio_wav_marker")
	}
	if probe.StockArchive.HasAudioWav {
		audio.State = "observed"
		audio.Evidence = append(audio.Evidence, "stock_archive_audio_wav")
	}
	if probe.Renderer.AudioConfMarker {
		audio.Evidence = append(audio.Evidence, "renderer_audio_conf_marker")
	}
	probe.Audio = audio
	return probe
}

func deviceProbesHandler(w http.ResponseWriter, r *http.Request) {
	if !prepareRequest(w, r, http.MethodGet) {
		return
	}
	if !requireAuthorization(w, r) {
		return
	}
	response := deviceIntelligenceResponse{
		Status:        "ok",
		SchemaVersion: 1,
		CollectedAt:   time.Now().UnixMilli(),
		System:        collectDeviceSystemProbe(),
		Boot:          collectDeviceBootProbe(),
	}
	writeJSON(w, http.StatusOK, response)
}
