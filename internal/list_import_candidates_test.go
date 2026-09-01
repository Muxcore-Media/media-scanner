package internal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
)

func TestListImportCandidatesIncludesAudioSkipsSamplesAndImported(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	tmp := t.TempDir()
	watch := filepath.Join(tmp, "downloads")
	lib := filepath.Join(tmp, "library")
	if err := os.MkdirAll(filepath.Join(watch, "extras"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"Radiohead - OK Computer - 01 Airbag.flac": []byte("audio"),
		"Movie.2020.1080p.mkv":                     []byte("video-content-here"),
		"sample.trailer.mkv":                       []byte("tiny"),
		filepath.Join("extras", "bonus.mkv"):       []byte("extra"),
	}
	for name, body := range files {
		p := filepath.Join(watch, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	imported := filepath.Join(watch, "Already.Done.2020.mkv")
	if err := os.WriteFile(imported, []byte("done"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: watch, LibraryPath: lib}); err != nil {
		t.Fatal(err)
	}
	if !m.importFile(imported, filepath.Base(imported), imported, "movie", lib, "") {
		t.Fatal("setup import failed")
	}

	resp, err := m.ListImportCandidates(ctx, &scannerv1.ListImportCandidatesRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range resp.GetCandidates() {
		paths = append(paths, c.GetPath())
	}
	if !containsPath(paths, filepath.Join(watch, "Radiohead - OK Computer - 01 Airbag.flac")) {
		t.Fatalf("expected audio candidate, got %v", paths)
	}
	if !containsPath(paths, filepath.Join(watch, "Movie.2020.1080p.mkv")) {
		t.Fatalf("expected video candidate, got %v", paths)
	}
	for _, p := range paths {
		base := strings.ToLower(filepath.Base(p))
		if strings.Contains(base, "sample") || strings.Contains(p, string(os.PathSeparator)+"extras"+string(os.PathSeparator)) {
			t.Fatalf("sample/extras should be omitted, got %v", paths)
		}
		if p == imported {
			t.Fatalf("already-imported path must be omitted: %s", p)
		}
	}
}

func containsPath(paths []string, want string) bool {
	want = filepath.Clean(want)
	for _, p := range paths {
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}
