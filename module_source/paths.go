package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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

	paths := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		targetPath := strings.TrimSpace(raw)
		if targetPath == "" {
			continue
		}
		cleaned, ok := validateTargetPath(targetPath)
		if !ok {
			return nil, errors.New("invalid saved path")
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
		if len(paths) > maxSavedPaths {
			return nil, errors.New("too many saved paths")
		}
	}

	if len(paths) == 0 {
		return nil, errors.New("no valid paths")
	}

	return paths, nil
}

func validateTargetPath(targetPath string) (string, bool) {
	if len(targetPath) == 0 || len(targetPath) > 512 || !strings.HasPrefix(targetPath, "/") || strings.ContainsRune(targetPath, '\x00') || strings.Contains(targetPath, "\\") {
		return "", false
	}

	cleaned := filepath.Clean(targetPath)
	if cleaned != targetPath || filepath.Base(cleaned) != "bootanimation.zip" {
		return "", false
	}

	allowedRoots := []string{"/system/", "/vendor/", "/product/", "/oem/", "/odm/", "/system_ext/", "/apex/", "/custom/"}
	for _, root := range allowedRoots {
		if strings.HasPrefix(cleaned, root) {
			return cleaned, true
		}
	}

	return "", false
}

func runPathRescan() ([]string, error) {
	cmd := exec.Command("/system/bin/sh", ModDir+"/rescan_paths.sh")
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return readSavedPaths()
}

func validatePreviewFile(previewPath string) error {
	f, err := os.Open(previewPath)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 512)
	n, readErr := f.Read(header)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if n < 4 {
		return errors.New("preview is empty or invalid")
	}

	contentType := http.DetectContentType(header[:n])
	if contentType != "video/webm" && !(header[0] == 0x1a && header[1] == 0x45 && header[2] == 0xdf && header[3] == 0xa3) {
		return errors.New("preview must be WebM")
	}
	return nil
}

func currentPathEnvironmentKey() string {
	for _, prop := range []string{"ro.build.fingerprint", "ro.build.display.id", "ro.build.id"} {
		if value := strings.TrimSpace(getPropValue(prop)); value != "" {
			return value
		}
	}
	return ""
}

func pathEnvironmentStatus() string {
	current := currentPathEnvironmentKey()
	if current == "" {
		return "unknown"
	}
	data, err := os.ReadFile(filepath.Join(ModDir, ".paths_environment"))
	if err != nil {
		return "unknown"
	}
	saved := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if saved == "" {
		return "unknown"
	}
	if secureEqual(saved, current) {
		return "current"
	}
	return "changed"
}
