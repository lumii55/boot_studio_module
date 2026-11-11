package main

import (
	"archive/zip"
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
)

type bootArchiveProbe struct {
	Status         string `json:"status"`
	Format         string `json:"format"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	FPS            int    `json:"fps,omitempty"`
	FrameCount     int    `json:"frame_count,omitempty"`
	Parts          int    `json:"parts,omitempty"`
	VideoCount     int    `json:"video_count,omitempty"`
	HasAudioWav    bool   `json:"has_audio_wav,omitempty"`
	HasAudioConfig bool   `json:"has_audio_config,omitempty"`
}

func archiveEntrySafe(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

const maxArchiveEntryCompressionRatio uint64 = 400

// Validate ZIP central-directory metadata once, before parsing descriptors or
// decoding media. The check is shared by uploads, history, device probes and
// generated previews. A ZIP whose metadata claims an extreme expansion cannot
// be sent to the root-owned Apply path.
func auditBootArchiveEntries(entries []*zip.File) error {
	if len(entries) == 0 || len(entries) > 10000 {
		return errors.New("invalid zip entry count")
	}
	seen := make(map[string]struct{}, len(entries))
	var totalUncompressed uint64
	for _, entry := range entries {
		if entry == nil || !archiveEntrySafe(entry.Name) ||
			(entry.Flags&0x1) != 0 || // encrypted ZIP entries are not supported
			(entry.Method != zip.Store && entry.Method != zip.Deflate) ||
			entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe zip entry")
		}
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return errors.New("zip entry path contains control characters")
		}
		parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
		for _, part := range parts {
			if part == "" || part == "." || part == ".." {
				return errors.New("noncanonical zip entry path")
			}
		}
		canonicalName := strings.TrimSuffix(name, "/")
		if _, exists := seen[canonicalName]; exists {
			return errors.New("duplicate zip entry path")
		}
		seen[canonicalName] = struct{}{}
		if entry.UncompressedSize64 > maxZipUncompressedSize-totalUncompressed {
			return errors.New("zip expands beyond the allowed size")
		}
		totalUncompressed += entry.UncompressedSize64
		if entry.Method == zip.Store && entry.CompressedSize64 != entry.UncompressedSize64 {
			return errors.New("invalid stored zip entry size")
		}
		if entry.UncompressedSize64 > 0 {
			if entry.CompressedSize64 == 0 {
				return errors.New("unsafe zip compression ratio")
			}
			if entry.Method == zip.Deflate {
				whole := entry.UncompressedSize64 / entry.CompressedSize64
				if whole > maxArchiveEntryCompressionRatio || (whole == maxArchiveEntryCompressionRatio && entry.UncompressedSize64%entry.CompressedSize64 != 0) {
					return errors.New("unsafe zip compression ratio")
				}
			}
		}
	}
	return nil
}

func inspectBootArchiveProbe(path string) bootArchiveProbe {
	probe := bootArchiveProbe{Status: "missing", Format: "unknown"}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return probe
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		probe.Status = "invalid"
		return probe
	}
	defer archive.Close()
	if err := auditBootArchiveEntries(archive.File); err != nil {
		probe.Status = "invalid"
		return probe
	}

	probe.Status = "ok"
	var descFile *zip.File
	hasVideoDesc := false
	var totalUncompressed uint64
	for _, entry := range archive.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if !archiveEntrySafe(name) {
			probe.Status = "invalid"
			probe.Format = "unknown"
			return probe
		}
		totalUncompressed += entry.UncompressedSize64
		if totalUncompressed > maxZipUncompressedSize {
			probe.Status = "invalid"
			probe.Format = "unknown"
			return probe
		}
		lower := strings.ToLower(name)
		if name == "desc.txt" {
			descFile = entry
		}
		if name == "videodesc.txt" {
			hasVideoDesc = true
		}
		if !strings.Contains(name, "/") && strings.HasSuffix(lower, ".mp4") {
			probe.VideoCount++
		}
		if strings.Contains(name, "/") && (strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg")) {
			probe.FrameCount++
		}
		if lower == "audio_conf.txt" {
			probe.HasAudioConfig = true
		}
		if lower == "audio.wav" || strings.HasSuffix(lower, "/audio.wav") {
			probe.HasAudioWav = true
		}
	}

	if descFile != nil && descFile.UncompressedSize64 <= 64<<10 {
		reader, openErr := descFile.Open()
		if openErr == nil {
			scanner := bufio.NewScanner(reader)
			if scanner.Scan() {
				fields := strings.Fields(scanner.Text())
				if len(fields) >= 3 {
					probe.Width, _ = strconv.Atoi(fields[0])
					probe.Height, _ = strconv.Atoi(fields[1])
					probe.FPS, _ = strconv.Atoi(fields[2])
				}
			}
			for scanner.Scan() {
				fields := strings.Fields(strings.TrimSpace(scanner.Text()))
				if len(fields) > 0 && (fields[0] == "p" || fields[0] == "c" || fields[0] == "f") {
					probe.Parts++
				}
			}
			_ = reader.Close()
		}
	}

	switch {
	case descFile != nil && probe.FrameCount > 0:
		probe.Format = "aosp_frames"
	case hasVideoDesc && probe.VideoCount > 0:
		probe.Format = "oem_video_sequence"
	case descFile != nil:
		probe.Format = "aosp_desc"
	default:
		probe.Format = "unknown_zip"
	}
	return probe
}

func inspectBootAnimation(zipPath, id string) (historyMetadata, error) {
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return historyMetadata{}, errors.New("invalid zip file")
	}
	defer archive.Close()

	if err := auditBootArchiveEntries(archive.File); err != nil {
		return historyMetadata{}, err
	}

	info, err := os.Stat(zipPath)
	if err != nil || info.IsDir() {
		return historyMetadata{}, errors.New("could not inspect zip file")
	}

	meta := historyMetadata{
		Version:   historyMetadataVersion,
		CreatedAt: historyCreatedAt(id),
		SizeBytes: info.Size(),
	}
	if meta.CreatedAt == 0 {
		meta.CreatedAt = info.ModTime().UnixMilli()
	}

	var descFile *zip.File
	var totalUncompressed uint64
	formats := make(map[string]struct{})

	for _, entry := range archive.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") {
			return historyMetadata{}, errors.New("unsafe zip path")
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return historyMetadata{}, errors.New("unsafe zip path")
			}
		}

		totalUncompressed += entry.UncompressedSize64
		if totalUncompressed > maxZipUncompressedSize {
			return historyMetadata{}, errors.New("zip expands beyond the allowed size")
		}

		if name == "desc.txt" {
			descFile = entry
		}
		lower := strings.ToLower(name)
		if strings.Contains(name, "/") {
			switch {
			case strings.HasSuffix(lower, ".png"):
				meta.FrameCount++
				formats["PNG"] = struct{}{}
			case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
				meta.FrameCount++
				formats["JPEG"] = struct{}{}
			}
		}
		if strings.HasSuffix(lower, "/audio.wav") || lower == "audio.wav" {
			meta.HasAudio = true
		}
	}

	if descFile == nil {
		return historyMetadata{}, errors.New("desc.txt not found")
	}
	if meta.FrameCount == 0 {
		return historyMetadata{}, errors.New("no animation frames found")
	}
	if descFile.UncompressedSize64 > 64<<10 {
		return historyMetadata{}, errors.New("desc.txt is too large")
	}

	descReader, err := descFile.Open()
	if err != nil {
		return historyMetadata{}, errors.New("could not read desc.txt")
	}
	defer descReader.Close()

	scanner := bufio.NewScanner(descReader)
	if !scanner.Scan() {
		return historyMetadata{}, errors.New("desc.txt is empty")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 3 {
		return historyMetadata{}, errors.New("invalid desc.txt header")
	}

	width, widthErr := strconv.Atoi(fields[0])
	height, heightErr := strconv.Atoi(fields[1])
	fps, fpsErr := strconv.Atoi(fields[2])
	if widthErr != nil || heightErr != nil || fpsErr != nil || width < 1 || height < 1 || fps < 1 || width > 16384 || height > 16384 || fps > 240 {
		return historyMetadata{}, errors.New("invalid desc.txt dimensions or fps")
	}
	meta.Width = width
	meta.Height = height
	meta.FPS = fps

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) > 0 && (parts[0] == "p" || parts[0] == "c") {
			meta.Parts++
		}
	}
	if err := scanner.Err(); err != nil {
		return historyMetadata{}, errors.New("could not parse desc.txt")
	}

	switch len(formats) {
	case 1:
		for format := range formats {
			meta.FrameFormat = format
		}
	default:
		meta.FrameFormat = "Mixed"
	}
	return meta, nil
}

func validateBootAnimation(zipPath string) error {
	_, err := inspectBootAnimation(zipPath, "")
	return err
}
