package internal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
)

func TestFsnotifyTriggersImport(t *testing.T) {
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "true") // plaintext gRPC listener for this test (meshtls dev flag)
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	m := NewModule(Config{
		DBPath:           filepath.Join(tmp, "scanner.db"),
		GRPCAddr:         ":0",
		ImportMode:       "move",
		MinVideoBytes:    -1,
		DebounceWait:     50 * time.Millisecond,
		SafetyRescan:     -1,
		InitialScanDelay: -1,
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })

	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{
		Path: srcDir, LibraryPath: libDir, MediaType: "movie",
	}); err != nil {
		t.Fatal(err)
	}

	srcFile := filepath.Join(srcDir, "Inception.2010.1080p.mkv")
	if err := os.WriteFile(srcFile, []byte("fake media"), 0644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	dest := filepath.Join(libDir, "Movies", "Inception (2010)", "Inception.2010.1080p.mkv")
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dest); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("file was not imported via fsnotify within timeout:", dest)
}

func TestIgnoreIncompleteWatchPath(t *testing.T) {
	if !ignoreIncompleteWatchPath("/downloads/show/ep.mkv.part") {
		t.Fatal("expected .part ignored")
	}
	if !ignoreIncompleteWatchPath("/downloads/ep.!ut") {
		t.Fatal("expected .!ut ignored")
	}
	if ignoreIncompleteWatchPath("/downloads/Inception.2010.1080p.mkv") {
		t.Fatal("media file should still trigger scans")
	}
}
