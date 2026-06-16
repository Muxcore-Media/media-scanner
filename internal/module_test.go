package internal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	scannerv1 "github.com/Muxcore-Media/media-scanner/proto/scannerv1"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()
	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "scanner.db"),
		GRPCAddr: ":0",
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { m.Stop(ctx) })
	return m
}

func TestModuleInfo(t *testing.T) {
	m := NewModule(Config{})
	info := m.Info()
	if info.ID == "" {
		t.Error("module ID must not be empty")
	}
	if info.Version == "" {
		t.Error("module version must not be empty")
	}
	if len(info.Capabilities) == 0 || info.Capabilities[0] != "media.scanner" {
		t.Errorf("expected media.scanner capability, got %v", info.Capabilities)
	}
}

func TestAddWatchDir(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	resp, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{
		Path:      tmp,
		MediaType: "movie",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Id == "" {
		t.Fatal("expected non-empty ID")
	}

	list, err := m.ListWatchDirs(ctx, &scannerv1.ListWatchDirsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Dirs) != 1 {
		t.Fatalf("expected 1 dir, got %d", len(list.Dirs))
	}
	if list.Dirs[0].Path != tmp {
		t.Errorf("expected path %s, got %s", tmp, list.Dirs[0].Path)
	}
	if list.Dirs[0].MediaType != "movie" {
		t.Errorf("expected media_type movie, got %s", list.Dirs[0].MediaType)
	}
}

func TestRemoveWatchDir(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	add, _ := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: t.TempDir()})
	_, err := m.RemoveWatchDir(ctx, &scannerv1.RemoveWatchDirRequest{Id: add.Id})
	if err != nil {
		t.Fatal(err)
	}

	list, _ := m.ListWatchDirs(ctx, &scannerv1.ListWatchDirsRequest{})
	if len(list.Dirs) != 0 {
		t.Errorf("expected 0 dirs after removal, got %d", len(list.Dirs))
	}
}

func TestImportFile(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "Fight.Club.1999.1080p.BluRay.mkv")
	if err := os.WriteFile(srcFile, []byte("fake media"), 0644); err != nil {
		t.Fatal(err)
	}

	libPath := filepath.Join(tmp, "library")
	result := m.importFile(srcFile, "Fight.Club.1999.1080p.BluRay.mkv", "movie", libPath)
	if !result {
		t.Fatal("expected import to succeed")
	}

	// Check the file was moved (title uses spaces, not dots)
	dest := filepath.Join(libPath, "Movies", "Fight Club (1999)", "Fight Club.1999.1080p.BluRay.mkv")
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		t.Fatal("imported file not found at destination:", dest)
	}

	// Original file no longer exists
	if _, err := os.Stat(srcFile); !os.IsNotExist(err) {
		t.Fatal("original file should have been moved")
	}
}

func TestImportTVShow(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "Breaking.Bad.S05E01.1080p.BluRay.mkv")
	if err := os.WriteFile(srcFile, []byte("fake tv"), 0644); err != nil {
		t.Fatal(err)
	}

	libPath := filepath.Join(tmp, "library")
	result := m.importFile(srcFile, "Breaking.Bad.S05E01.1080p.BluRay.mkv", "tv", libPath)
	if !result {
		t.Fatal("expected TV import to succeed")
	}

	destFile := filepath.Join(libPath, "TV", "Breaking Bad", "Season 05", "Breaking Bad.S05E01.1080p.BluRay.mkv")
	if _, err := os.Stat(destFile); os.IsNotExist(err) {
		t.Fatal("imported TV file not found at destination:", destFile)
	}
}

func TestScanDirectory(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")

	os.MkdirAll(srcDir, 0755)

	files := []string{
		"Fight.Club.1999.1080p.BluRay.mkv",
		"Inception.2010.1080p.WEB-DL.mp4",
		"Breaking.Bad.S05E01.1080p.BluRay.mkv",
		"note.txt",
	}
	for _, f := range files {
		os.WriteFile(filepath.Join(srcDir, f), []byte(f), 0644)
	}

	found, imported, skipped := m.scanDirectory(srcDir, "both", libDir)
	if found != 3 {
		t.Errorf("expected 3 media files found, got %d", found)
	}
	if imported != 3 {
		t.Errorf("expected 3 imported, got %d", imported)
	}
	if skipped != 0 {
		t.Errorf("expected 0 skipped, got %d", skipped)
	}

	// Second scan: files were moved away, so nothing to find
	found2, imported2, skipped2 := m.scanDirectory(srcDir, "both", libDir)
	if found2 != 0 {
		t.Errorf("expected 0 found on second scan (files moved), got %d", found2)
	}
	if imported2 != 0 {
		t.Errorf("expected 0 imported on second scan, got %d", imported2)
	}
	if skipped2 != 0 {
		t.Errorf("expected 0 skipped on second scan, got %d", skipped2)
	}
}

func TestScanCommand(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")

	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "Test.Movie.2020.1080p.mkv"), []byte("data"), 0644)

	m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir})

	resp, err := m.Scan(ctx, &scannerv1.ScanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesFound != 1 {
		t.Errorf("expected 1 found, got %d", resp.FilesFound)
	}
	if resp.FilesImported != 1 {
		t.Errorf("expected 1 imported, got %d", resp.FilesImported)
	}
}

func TestListImported(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	imported, err := m.ListImported(ctx, &scannerv1.ListImportedRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Total != 0 {
		t.Errorf("expected 0 imported, got %d", imported.Total)
	}
}

func TestGetStats(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	stats, err := m.GetStats(ctx, &scannerv1.GetStatsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.WatchDirs != 0 {
		t.Errorf("expected 0 watch dirs, got %d", stats.WatchDirs)
	}
}

func TestHealth(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	if err := m.Health(ctx); err != nil {
		t.Fatal("expected health to pass after init")
	}
}

func TestLifecycle(t *testing.T) {
	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "lifecycle.db"),
		GRPCAddr: ":0",
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestParseFileNameMovie(t *testing.T) {
	tests := []struct {
		input   string
		title   string
		year    int
		media   string
		quality string
	}{
		{"Fight.Club.1999.1080p.BluRay.mkv", "Fight Club", 1999, "movie", "1080p.BluRay"},
		{"The.Matrix.1999.720p.WEB-DL.mp4", "The Matrix", 1999, "movie", "720p.WEB-DL"},
		{"Inception.2010.2160p.4K.Remux.mkv", "Inception", 2010, "movie", "2160p.4K.Remux"},
		{"Movie Test 2020.mp4", "Movie Test", 2020, "movie", ""},
	}
	for _, tt := range tests {
		p := parseFileName(tt.input)
		if p.Title != tt.title {
			t.Errorf("parseFileName(%q) title = %q, want %q", tt.input, p.Title, tt.title)
		}
		if p.Year != tt.year {
			t.Errorf("parseFileName(%q) year = %d, want %d", tt.input, p.Year, tt.year)
		}
		if p.MediaType != tt.media {
			t.Errorf("parseFileName(%q) mediaType = %q, want %q", tt.input, p.MediaType, tt.media)
		}
	}
}

func TestParseFileNameTV(t *testing.T) {
	tests := []struct {
		input   string
		title   string
		season  int
		episode int
		media   string
	}{
		{"Breaking.Bad.S05E01.1080p.BluRay.mkv", "Breaking Bad", 5, 1, "tv"},
		{"Game.of.Thrones.S01E10.720p.HDTV.mkv", "Game of Thrones", 1, 10, "tv"},
		{"Show.Name.1x05.1080p.mkv", "Show Name", 1, 5, "tv"},
		{"show.name.s02e03.720p.WEB-DL.mp4", "show name", 2, 3, "tv"},
	}
	for _, tt := range tests {
		p := parseFileName(tt.input)
		if p.Title != tt.title {
			t.Errorf("parseFileName(%q) title = %q, want %q", tt.input, p.Title, tt.title)
		}
		if p.Season != tt.season {
			t.Errorf("parseFileName(%q) season = %d, want %d", tt.input, p.Season, tt.season)
		}
		if p.Episode != tt.episode {
			t.Errorf("parseFileName(%q) episode = %d, want %d", tt.input, p.Episode, tt.episode)
		}
		if p.MediaType != tt.media {
			t.Errorf("parseFileName(%q) mediaType = %q, want %q", tt.input, p.MediaType, tt.media)
		}
	}
}

func TestIsMediaExt(t *testing.T) {
	exts := []string{".mkv", ".mp4", ".avi", ".m4v", ".mov"}
	for _, e := range exts {
		if !isMediaExt(e) {
			t.Errorf("expected %s to be a media extension", e)
		}
	}
	if isMediaExt(".txt") {
		t.Error("expected .txt not to be a media extension")
	}
	if isMediaExt(".srt") {
		t.Error("expected .srt not to be a media extension")
	}
}
