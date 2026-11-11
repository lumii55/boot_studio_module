package main

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeSecurityArchive(t *testing.T, specs []struct {
	name   string
	data   string
	method uint16
	mode   os.FileMode
}) []*zip.File {
	t.Helper()
	var b bytes.Buffer
	writer := zip.NewWriter(&b)
	for _, spec := range specs {
		header := &zip.FileHeader{Name: spec.name, Method: spec.method}
		if spec.mode != 0 {
			header.SetMode(spec.mode)
		}
		w, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(spec.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return reader.File
}

func TestArchivePreflightRejectsHostileMetadata(t *testing.T) {
	type spec = struct {
		name   string
		data   string
		method uint16
		mode   os.FileMode
	}
	valid := []spec{{name: "desc.txt", data: "96 64 30\np 0 0 part0\n", method: zip.Store}, {name: "part0/00000.png", data: "PNGTEST", method: zip.Store}}
	cases := []struct {
		name   string
		mutate func([]*zip.File) []*zip.File
		fail   bool
	}{
		{"valid", nil, false},
		{"traversal", func(f []*zip.File) []*zip.File { f[1].Name = "../outside"; return f }, true},
		{"absolute", func(f []*zip.File) []*zip.File { f[1].Name = "/outside"; return f }, true},
		{"backslash-traversal", func(f []*zip.File) []*zip.File { f[1].Name = "part0\\..\\outside"; return f }, true},
		{"dot-segment", func(f []*zip.File) []*zip.File { f[1].Name = "part0/./image.png"; return f }, true},
		{"empty-segment", func(f []*zip.File) []*zip.File { f[1].Name = "part0//image.png"; return f }, true},
		{"duplicate", func(f []*zip.File) []*zip.File { f[1].Name = "desc.txt"; return f }, true},
		{"unsupported-compression", func(f []*zip.File) []*zip.File { f[1].Method = 93; return f }, true},
		{"encrypted-flag", func(f []*zip.File) []*zip.File { f[1].Flags |= 1; return f }, true},
		{"uncompressed-limit", func(f []*zip.File) []*zip.File { f[1].UncompressedSize64 = maxZipUncompressedSize + 1; return f }, true},
		{"ratio-bomb", func(f []*zip.File) []*zip.File {
			f[1].Method = zip.Deflate
			f[1].UncompressedSize64 = 2000000
			f[1].CompressedSize64 = 100
			return f
		}, true},
		{"zero-compressed-size", func(f []*zip.File) []*zip.File { f[1].UncompressedSize64 = 500; f[1].CompressedSize64 = 0; return f }, true},
		{"symlink", func(f []*zip.File) []*zip.File { f[1].FileHeader.SetMode(os.ModeSymlink | 0777); return f }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := makeSecurityArchive(t, valid)
			if tc.mutate != nil {
				files = tc.mutate(files)
			}
			err := auditBootArchiveEntries(files)
			if (err != nil) != tc.fail {
				t.Fatalf("expected fail=%v got %v", tc.fail, err)
			}
		})
	}
}

func TestStoredTrustPermissionsFailClosed(t *testing.T) {
	cases := []struct {
		version      int
		stored, want string
		reject       bool
	}{
		{1, "", "admin", false}, // documented v1 migration
		{1, "control", "control", false},
		{2, "VIEW", "view", false},
		{2, "admin", "admin", false},
		{2, "", "", true},
		{2, "superadmin", "", true},
		{1, "superadmin", "", true},
		{2, "all", "", true},
	}
	for _, tc := range cases {
		actual, err := normalizeStoredTrustPermission(tc.version, tc.stored)
		if (err != nil) != tc.reject || (!tc.reject && actual != tc.want) {
			t.Errorf("version=%d stored=%q got=%q err=%v", tc.version, tc.stored, actual, err)
		}
	}
}

func TestCompanionOriginWhitelist(t *testing.T) {
	good := []string{"", "https://lumii55.github.io", "http://localhost:4444", "http://127.0.0.1:4040"}
	bad := []string{"https://lumii55.github.io.evil.test", "https://evil.test", "http://evil.test:4040", "https://lumii55.github.io:4443", "null", "file://", "http://localhost.evil.test"}
	for _, origin := range good {
		if !isAllowedOrigin(origin) {
			t.Errorf("legitimate origin rejected: %s", origin)
		}
	}
	for _, origin := range bad {
		if isAllowedOrigin(origin) {
			t.Errorf("hostile origin allowed: %s", origin)
		}
	}
}

func TestCompanionRequestRejectsHostileOriginAndMethods(t *testing.T) {
	cases := []struct {
		origin, method string
		want           int
	}{
		{"https://evil.test", http.MethodPost, http.StatusForbidden},
		{"https://lumii55.github.io", http.MethodDelete, http.StatusMethodNotAllowed},
		{"https://lumii55.github.io", http.MethodOptions, http.StatusNoContent},
		{"https://lumii55.github.io", http.MethodPost, 0},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, "http://127.0.0.1:4040/remove", nil)
		req.Header.Set("Origin", tc.origin)
		rec := httptest.NewRecorder()
		proceed := prepareRequest(rec, req, http.MethodPost)
		if tc.want == 0 {
			if !proceed {
				t.Errorf("post erroneously rejected")
			}
			continue
		}
		if proceed || rec.Code != tc.want {
			t.Errorf("origin=%s method=%s proceed=%v status=%d want %d", tc.origin, tc.method, proceed, rec.Code, tc.want)
		}
	}
}

func TestPrivilegedEndpointsRequireAuthorization(t *testing.T) {
	endpoints := []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/upload", uploadHandler}, {"/remove", removeHandler}, {"/reset", resetHandler}, {"/rescan", rescanHandler}, {"/factory_reset", factoryResetHandler},
		{"/history/apply", historyApplyHandler}, {"/playlist/apply", playlistApplyHandler}, {"/test/apply", testApplyHandler},
	}
	for _, tc := range endpoints {
		t.Run(strings.TrimPrefix(tc.path, "/"), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://192.168.1.25:4040"+tc.path, nil)
			req.RemoteAddr = "192.168.1.44:53124"
			req.Header.Set("Origin", "https://lumii55.github.io")
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unauthorized call status=%d body=%q", rec.Code, rec.Body.String())
			}
		})
	}
}

// Disk-backed integration: the same zip.OpenReader and metadata parser used by
// uploads and History, but never invokes root, shell, or the Android boot path.
func TestBootArchiveIngressFromActualZip(t *testing.T) {
	for _, tc := range []struct {
		name    string
		zipName string
		bad     bool
	}{
		{"valid", "part0/00000.png", false},
		{"parent-traversal", "../outside.png", true},
		{"absolute-path", "/outside.png", true},
		{"dot-component", "part0/./00000.png", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bootanimation.zip")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(f)
			for _, v := range []struct{ name, content string }{{"desc.txt", "96 64 30\np 0 0 part0\n"}, {tc.zipName, "PNG placeholder for structure test"}} {
				w, err := writer.Create(v.name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = w.Write([]byte(v.content)); err != nil {
					t.Fatal(err)
				}
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = inspectBootAnimation(path, "")
			if (err != nil) != tc.bad {
				t.Errorf("bad=%v err=%v", tc.bad, err)
			}
		})
	}
}
