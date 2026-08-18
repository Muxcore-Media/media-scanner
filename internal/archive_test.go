package internal

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-5 * time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveKind(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		isFirst bool
	}{
		{"movie.zip", "zip", true},
		{"show.part1.rar", "rar", true},
		{"show.part01.rar", "rar", true},
		{"show.part2.rar", "rar", false},
		{"show.rar", "rar", true},
		{"show.r00", "rar", false},
		{"video.mkv", "", false},
	}
	for _, tc := range cases {
		kind, first := archiveKind(tc.name)
		if kind != tc.kind || first != tc.isFirst {
			t.Errorf("%s: got (%s,%v) want (%s,%v)", tc.name, kind, first, tc.kind, tc.isFirst)
		}
	}
}

func TestSafeExtractPathRejectsZipSlip(t *testing.T) {
	dest := t.TempDir()
	_, err := safeExtractPath(dest, "../evil.txt")
	if err == nil {
		t.Fatal("expected zip slip rejection")
	}
	_, err = safeExtractPath(dest, "ok/file.txt")
	if err != nil {
		t.Fatal(err)
	}
}

func TestExtractZipBeforeImport(t *testing.T) {
	m := newTestModule(t)
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(srcDir, "Fight.Club.1999.1080p.BluRay.zip")
	writeTestZip(t, zipPath, map[string][]byte{
		"Fight.Club.1999.1080p.BluRay.mkv": []byte("fake media"),
	})

	found, imported, skipped := m.scanDirectory(srcDir, "movie", libDir, "")
	if found != 1 {
		t.Fatalf("found=%d imported=%d skipped=%d", found, imported, skipped)
	}
	if imported != 1 {
		t.Fatalf("expected 1 imported, got %d", imported)
	}
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Fatal("zip should be removed after successful extract")
	}
	dest := filepath.Join(libDir, "Movies", "Fight Club (1999)", "Fight Club.1999.1080p.BluRay.mkv")
	if _, err := os.Stat(dest); err != nil {
		t.Fatal("imported file missing:", dest)
	}
}

func TestExtractZipSlipRejected(t *testing.T) {
	m := newTestModule(t)
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "evil.zip")
	writeTestZip(t, zipPath, map[string][]byte{
		"../outside.mkv": []byte("nope"),
	})
	_, err := m.maybeExtractArchive(zipPath)
	if err == nil {
		t.Fatal("expected zip slip error")
	}
	if !strings.Contains(err.Error(), "zip slip") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatal("archive should remain on failure")
	}
}

func TestArchiveUnstableSkipped(t *testing.T) {
	m := newTestModule(t)
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "fresh.zip")
	writeTestZip(t, zipPath, map[string][]byte{"a.mkv": []byte("x")})
	now := time.Now()
	if err := os.Chtimes(zipPath, now, now); err != nil {
		t.Fatal(err)
	}
	dest, err := m.maybeExtractArchive(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if dest != "" {
		t.Fatal("unstable archive should not extract yet")
	}
}
