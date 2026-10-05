package internal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/pathguard"
)

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportPathRefusesEscapes(t *testing.T) {
	m := newTestModule(t) // ImportMode "move": an escape would move the outside file
	ctx := context.Background()
	base := t.TempDir()
	watch := filepath.Join(base, "downloads")
	sibling := filepath.Join(base, "downloads2")
	lib := filepath.Join(base, "library")
	outside := t.TempDir()
	mkdirs(t, watch, sibling, lib)
	secret := filepath.Join(outside, "Secret.Movie.2001.1080p.mkv")
	writeFile(t, secret)
	siblingFile := filepath.Join(sibling, "Other.Movie.2002.1080p.mkv")
	writeFile(t, siblingFile)
	if err := os.Symlink(secret, filepath.Join(watch, "Linked.Movie.2003.1080p.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(watch, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: watch, LibraryPath: lib}); err != nil {
		t.Fatal(err)
	}

	for name, p := range map[string]string{
		"symlinked file":   filepath.Join(watch, "Linked.Movie.2003.1080p.mkv"),
		"symlinked dir":    filepath.Join(watch, "escape"),
		"through dir link": filepath.Join(watch, "escape", filepath.Base(secret)),
		"sibling prefix":   siblingFile,
		"dotdot absolute":  watch + "/../downloads2/" + filepath.Base(siblingFile),
		"dotdot relative":  "../downloads2/" + filepath.Base(siblingFile),
		"outside absolute": secret,
		"filesystem root":  "/",
		"etc":              "/etc/passwd",
	} {
		_, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: p})
		if err == nil || !strings.Contains(err.Error(), "watch") {
			t.Errorf("%s (%s): want watch-dir refusal, got %v", name, p, err)
		}
	}
	for _, p := range []string{secret, siblingFile} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was touched by a refused import: %v", p, err)
		}
	}
}

func TestConfinedWatchDirDeepest(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "dl")
	inner := filepath.Join(outer, "tv")
	mkdirs(t, inner)
	dirs := []watchDirEntry{{path: outer, mediaType: "movie"}, {path: inner, mediaType: "tv"}}
	d, ok := confinedWatchDir(dirs, filepath.Join(inner, "Show.S01E01.mkv"))
	if !ok || d.mediaType != "tv" {
		t.Fatalf("got %+v ok=%v", d, ok)
	}
	if _, ok := confinedWatchDir(dirs, base+"/dl2/x.mkv"); ok {
		t.Fatal("sibling prefix matched")
	}
}

func newConfinedScanner(t *testing.T, lib string) *Module {
	t.Helper()
	t.Setenv("SCANNER_ALLOWED_ROOTS", "")
	m := NewModule(Config{
		DBPath:        filepath.Join(t.TempDir(), "scanner.db"),
		GRPCAddr:      ":0",
		LibraryRoot:   lib,
		MinVideoBytes: -1,
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	return m
}

func TestWatchDirAndSettingPathsConfined(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "media")
	sibling := filepath.Join(base, "media2")
	outside := t.TempDir()
	mkdirs(t, filepath.Join(lib, "downloads"), filepath.Join(lib, "movies"), sibling)
	escape := filepath.Join(lib, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	m := newConfinedScanner(t, lib)
	ctx := context.Background()

	bad := map[string]string{
		"root": "/", "etc": "/etc", "outside": outside, "sibling prefix": sibling,
		"symlink escape": escape, "dotdot": lib + "/../media2",
	}
	for name, p := range bad {
		for _, req := range []*scannerv1.AddWatchDirRequest{
			{Path: p},
			{Path: filepath.Join(lib, "downloads"), LibraryPath: p},
			{Path: filepath.Join(lib, "downloads"), TvLibraryPath: p},
			{Path: filepath.Join(lib, "downloads"), MusicLibraryPath: p},
		} {
			if _, err := m.AddWatchDir(ctx, req); !errors.Is(err, pathguard.ErrOutsideRoots) && !errors.Is(err, pathguard.ErrInvalidPath) {
				t.Errorf("AddWatchDir %s %+v: want refusal, got %v", name, req, err)
			}
		}
		if err := m.UpdateSetting("library_root", p); err == nil {
			t.Errorf("library_root %s: want refusal", name)
		}
		if err := m.UpdateSetting("tv_library_root", p); err == nil {
			t.Errorf("tv_library_root %s: want refusal", name)
		}
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: "relative/dir"}); err == nil {
		t.Error("relative watch dir accepted")
	}

	add, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{
		Path: filepath.Join(lib, "downloads"), LibraryPath: filepath.Join(lib, "movies"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateWatchDir(ctx, &scannerv1.UpdateWatchDirRequest{Id: add.Id, LibraryPath: escape}); !errors.Is(err, pathguard.ErrOutsideRoots) {
		t.Fatalf("UpdateWatchDir escape: want ErrOutsideRoots, got %v", err)
	}
	if _, err := m.UpdateWatchDir(ctx, &scannerv1.UpdateWatchDirRequest{Id: add.Id, Path: "/etc"}); !errors.Is(err, pathguard.ErrOutsideRoots) {
		t.Fatalf("UpdateWatchDir /etc: want ErrOutsideRoots, got %v", err)
	}
	if err := m.UpdateSetting("tv_library_root", filepath.Join(lib, "tv")); err != nil {
		t.Fatalf("tv_library_root inside: %v", err)
	}
	if err := m.UpdateSetting("tv_library_root", ""); err != nil {
		t.Fatalf("clearing tv_library_root: %v", err)
	}
}

func TestBaseRootsFromConfig(t *testing.T) {
	sep := string(os.PathListSeparator)
	t.Setenv("SCANNER_ALLOWED_ROOTS", "/srv/dl/"+sep+"relative"+sep+" "+sep+"/data/media")
	got := baseRootsFromConfig(Config{LibraryRoot: "/data/media", TVLibraryRoot: "/data/tv", DefaultWatchDir: "rel", AllowedRoots: []string{"/extra"}})
	want := []string{"/data/media", "/data/tv", "/extra", "/srv/dl"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}
