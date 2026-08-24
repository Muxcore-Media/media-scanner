package internal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
)

func TestImportPathOutsideWatchDirListsRoots(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	watch := t.TempDir()
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: watch}); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	_, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: outside})
	if err == nil {
		t.Fatal("expected error for path outside watch dirs")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not under any registered watch directory") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, watch) {
		t.Fatalf("expected watch root in error, got: %v", err)
	}
}

func TestImportPathAlreadyImportedCountsAsSuccess(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	src := filepath.Join(srcDir, "Fight.Club.1999.1080p.mkv")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}
	if !m.importFile(src, filepath.Base(src), "movie", libDir, "") {
		t.Fatal("setup import failed")
	}

	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: src})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesImported != 1 {
		t.Fatalf("already-imported should count as success for automation: %+v", resp)
	}
}

func TestImportPathDirectoryZeroImportedError(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	savePath := filepath.Join(srcDir, "pack")
	if err := os.MkdirAll(savePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(savePath, "RARBG.mkv"), []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}

	_, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: savePath})
	if err == nil {
		t.Fatal("expected error when media found but none imported")
	}
	if !strings.Contains(err.Error(), "imported 0") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportPathContextCanceled(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	deep := filepath.Join(srcDir, "a", "b", "c", "d", "e", "f", "g", "h")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		f := filepath.Join(deep, "Movie.Part"+string(rune('A'+i%26))+".2020.1080p.mkv")
		if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.AddWatchDir(context.Background(), &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: srcDir})
	if err == nil {
		t.Fatal("expected canceled context error")
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportPathZeroImportedErrorHelper(t *testing.T) {
	if err := importPathZeroImportedError("/dl/x", 2, 0, 2); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "found 2 media file(s)") {
		t.Fatalf("unexpected: %v", err)
	}
	if err := importPathZeroImportedError("/dl/x", 1, 1, 0); err != nil {
		t.Fatalf("success path should not error: %v", err)
	}
}

func TestImportPathSingleFileRejectedError(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	src := filepath.Join(srcDir, "RARBG.mkv")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}

	_, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: src})
	if err == nil {
		t.Fatal("expected single-file import failure error")
	}
	if !strings.Contains(err.Error(), "failed to import") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportPathRelativeUnresolvedError(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	watch := t.TempDir()
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: watch}); err != nil {
		t.Fatal(err)
	}

	_, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: "partials/missing/file.mkv"})
	if err == nil {
		t.Fatal("expected unresolved relative path error")
	}
	if !strings.Contains(err.Error(), "could not be resolved") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportPathHonorsParentContextTimeout(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "Test.Movie.2020.1080p.mkv"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(context.Background(), &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	_, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: srcDir})
	if err == nil {
		t.Fatal("expected deadline exceeded")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("unexpected error: %v", err)
	}
}
