package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func stagedAnimationPath() string {
	return filepath.Join(ModDir, "staging", "bootanimation.zip")
}

func stagedMetadata() (historyMetadata, error) {
	path := stagedAnimationPath()
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return historyMetadata{}, errors.New("no staged animation")
	}
	return inspectBootAnimation(path, "")
}

func setSystemFileContext(path string) error {
	command := "chcon u:object_r:system_file:s0 " + shellQuote(path)
	output, err := exec.Command("/system/bin/sh", "-c", command).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func previewShadowPath(targetPath string) string {
	return ModDir + targetPath + ".bas-test-preview"
}

func cleanupPreviewShadows() {
	paths, err := readSavedPaths()
	if err != nil {
		return
	}
	for _, targetPath := range paths {
		_ = os.Remove(previewShadowPath(targetPath))
		_ = os.Remove(previewShadowPath(targetPath) + ".tmp")
	}
}

func prepareStagedPreviewSource() (string, error) {
	paths, err := readSavedPaths()
	if err != nil {
		return "", errors.New("no valid saved path found")
	}
	stagePath := stagedAnimationPath()
	if err := validateBootAnimation(stagePath); err != nil {
		return "", errors.New("no valid staged animation")
	}

	shadowPath := previewShadowPath(paths[0])
	if err := os.MkdirAll(filepath.Dir(shadowPath), 0755); err != nil {
		return "", errors.New("could not prepare preview source directory")
	}
	tempPath := shadowPath + ".tmp"
	_ = os.Remove(tempPath)
	_ = os.Remove(shadowPath)
	if err := linkOrCopyFile(stagePath, tempPath, 0644); err != nil {
		return "", errors.New("could not prepare staged preview source")
	}
	if err := setSystemFileContext(tempPath); err != nil {
		_ = os.Remove(tempPath)
		return "", errors.New("could not label staged preview for boot animation service")
	}
	if err := os.Rename(tempPath, shadowPath); err != nil {
		_ = os.Remove(tempPath)
		return "", errors.New("could not commit staged preview source")
	}
	if err := os.Chmod(shadowPath, 0644); err != nil {
		_ = os.Remove(shadowPath)
		return "", errors.New("could not set staged preview permissions")
	}
	return shadowPath, nil
}

func mountNamespaceID(path string) string {
	value, err := os.Readlink(path)
	if err != nil {
		return "unknown"
	}
	return value
}

func globalMountCommand(args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("missing global mount command")
	}
	selfNamespace := mountNamespaceID("/proc/self/ns/mnt")
	initNamespace := mountNamespaceID("/proc/1/ns/mnt")
	if selfNamespace != "unknown" && selfNamespace == initNamespace {
		return exec.Command(args[0], args[1:]...).CombinedOutput()
	}
	if info, err := os.Stat("/system/bin/nsenter"); err == nil && !info.IsDir() {
		cmdArgs := append([]string{"-t", "1", "-m"}, args...)
		return exec.Command("/system/bin/nsenter", cmdArgs...).CombinedOutput()
	}
	if info, err := os.Stat("/system/bin/toybox"); err == nil && !info.IsDir() {
		cmdArgs := append([]string{"nsenter", "-t", "1", "-m"}, args...)
		return exec.Command("/system/bin/toybox", cmdArgs...).CombinedOutput()
	}
	if nsenterPath, err := exec.LookPath("nsenter"); err == nil {
		cmdArgs := append([]string{"-t", "1", "-m"}, args...)
		return exec.Command(nsenterPath, cmdArgs...).CombinedOutput()
	}
	return nil, errors.New("global mount namespace unavailable")
}

func runGlobalMountCommand(args ...string) error {
	output, err := globalMountCommand(args...)
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = err.Error()
	}
	return errors.New(message)
}

func decodeMountInfoPath(value string) string {
	replacer := strings.NewReplacer(`\\040`, " ", `\\011`, "\t", `\\012`, "\n", `\\134`, `\\`)
	return replacer.Replace(value)
}

func globalMountCount(targetPath string) (int, error) {
	output, err := globalMountCommand("/system/bin/cat", "/proc/self/mountinfo")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && decodeMountInfoPath(fields[4]) == targetPath {
			count++
		}
	}
	return count, nil
}

func globalRegularFileExists(path string) bool {
	return runGlobalMountCommand("/system/bin/test", "-f", path) == nil
}

func globalFilesMatch(sourcePath, targetPath string) bool {
	return runGlobalMountCommand("/system/bin/cmp", "-s", sourcePath, targetPath) == nil
}

func previewMountMarkerPath() string {
	return filepath.Join(ModDir, ".preview_mounts")
}

func writePreviewMountMarker(paths []string) {
	if len(paths) == 0 {
		_ = os.Remove(previewMountMarkerPath())
		return
	}
	_ = os.WriteFile(previewMountMarkerPath(), []byte(strings.Join(paths, "\n")+"\n"), 0600)
}

func readPreviewMountMarker() []string {
	data, err := os.ReadFile(previewMountMarkerPath())
	if err != nil {
		return nil
	}
	paths := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		path := strings.TrimSpace(raw)
		cleaned, ok := validateTargetPath(path)
		if !ok {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
	}
	return paths
}

func unmountPreviewTargets(paths []string) {
	for i := len(paths) - 1; i >= 0; i-- {
		_ = runGlobalMountCommand("/system/bin/umount", paths[i])
	}
	writePreviewMountMarker(nil)
}

func cleanupOrphanPreviewMounts() {
	paths := readPreviewMountMarker()
	if len(paths) > 0 {
		unmountPreviewTargets(paths)
	}
}

func currentPreviewState() (bool, []string) {
	previewMu.Lock()
	defer previewMu.Unlock()
	return previewActive, append([]string(nil), previewTargetPaths...)
}

func acquirePreview(targetPaths []string) (chan struct{}, bool) {
	previewMu.Lock()
	defer previewMu.Unlock()
	if previewActive {
		return nil, false
	}
	previewActive = true
	previewTargetPaths = append([]string(nil), targetPaths...)
	previewCancel = make(chan struct{})
	return previewCancel, true
}

func releasePreview(cancel chan struct{}) {
	previewMu.Lock()
	if previewActive && previewCancel == cancel {
		previewActive = false
		previewTargetPaths = nil
		previewCancel = nil
	}
	previewMu.Unlock()
}

func cleanupPreview(targetPaths []string, cancel chan struct{}) {
	_ = exec.Command("/system/bin/setprop", "ctl.stop", "bootanim").Run()
	time.Sleep(1 * time.Second)
	unmountPreviewTargets(targetPaths)
	releasePreview(cancel)
}

func cancelActivePreview() bool {
	previewMu.Lock()
	if !previewActive || previewCancel == nil {
		previewMu.Unlock()
		return false
	}
	cancel := previewCancel
	select {
	case <-cancel:
	default:
		close(cancel)
	}
	previewMu.Unlock()
	return true
}

func waitForPreviewStop(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		active, _ := currentPreviewState()
		if !active {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func clearStagedTestState() error {
	if cancelActivePreview() {
		waitForPreviewStop(3 * time.Second)
	}
	cleanupOrphanPreviewMounts()
	cleanupPreviewShadows()
	if err := os.Remove(stagedAnimationPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
