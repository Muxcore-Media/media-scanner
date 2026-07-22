package internal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scannerv1 "github.com/Muxcore-Media/media-scanner/proto/scannerv1"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()
	m := NewModule(Config{
		DBPath:        filepath.Join(t.TempDir(), "scanner.db"),
		GRPCAddr:      ":0",
		ImportMode:    "move",
		MinVideoBytes: -1,
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

func TestImportFileWithSidecarSubtitle(t *testing.T) {
	m := newTestModule(t)

	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "Inception.2010.1080p.mkv")
	srcSub := filepath.Join(tmp, "Inception.2010.1080p.en.srt")
	if err := os.WriteFile(srcFile, []byte("fake media"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcSub, []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	libPath := filepath.Join(tmp, "library")
	if !m.importFile(srcFile, "Inception.2010.1080p.mkv", "movie", libPath) {
		t.Fatal("expected import to succeed")
	}

	destSub := filepath.Join(libPath, "Movies", "Inception (2010)", "Inception.2010.1080p.en.srt")
	if _, err := os.Stat(destSub); os.IsNotExist(err) {
		t.Fatal("sidecar subtitle not imported:", destSub)
	}
	if _, err := os.Stat(srcSub); !os.IsNotExist(err) {
		t.Fatal("source subtitle should have been removed")
	}
}

func TestImportDegradesWithoutCapabilities(t *testing.T) {
	m := newTestModule(t)
	// mc is nil: rename/ffprobe/subtitles discovery no-ops; local import still works.
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "Dune.2021.2160p.mkv")
	if err := os.WriteFile(srcFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	libPath := filepath.Join(tmp, "library")
	if !m.importFile(srcFile, "Dune.2021.2160p.mkv", "movie", libPath) {
		t.Fatal("import should succeed without renamer/ffprobe")
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

func TestParseFileNameMultiEpAndAbsolute(t *testing.T) {
	p := parseFileName("Show.Name.S01E01-E03.1080p.mkv")
	if p.MediaType != "tv" || p.Season != 1 || len(p.Episodes) != 3 {
		t.Fatalf("multi-ep range: %+v", p)
	}
	if p.Episodes[0] != 1 || p.Episodes[2] != 3 {
		t.Fatalf("episodes: %v", p.Episodes)
	}

	p = parseFileName("Show.Name.S01E01E02.mkv")
	if len(p.Episodes) != 2 || p.Episodes[1] != 2 {
		t.Fatalf("multi-ep chain: %v", p.Episodes)
	}

	p = parseFileName("Anime.Title.-.150.mkv")
	if p.AbsoluteNumber != 150 || p.MediaType != "tv" {
		t.Fatalf("absolute dash: %+v", p)
	}

	p = parseFileName("Anime.Title.[042].mkv")
	if p.AbsoluteNumber != 42 {
		t.Fatalf("absolute bracket: %d", p.AbsoluteNumber)
	}

	p = parseFileName("Show.S01.Complete.1080p.mkv")
	if !p.SeasonPack {
		t.Fatal("expected season pack flag")
	}

	p = parseFileName("Daily.Show.2024.03.15.720p.mkv")
	if p.MediaType != "tv" || p.AirDate != "2024-03-15" {
		t.Fatalf("air date: %+v", p)
	}
	if !strings.Contains(strings.ToLower(p.Title), "daily") {
		t.Fatalf("air date title: %q", p.Title)
	}

	if tag := formatEpisodeTag(1, []int{1, 2, 3}, 0); tag != "S01E01-E03" {
		t.Fatalf("tag=%s", tag)
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

func TestImportHardlinkKeepsSource(t *testing.T) {
	m := NewModule(Config{
		DBPath:        filepath.Join(t.TempDir(), "scanner.db"),
		GRPCAddr:      ":0",
		ImportMode:    "hardlink",
		MinVideoBytes: -1,
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(ctx) })

	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "Fight.Club.1999.1080p.BluRay.mkv")
	if err := os.WriteFile(srcFile, []byte("fake media"), 0644); err != nil {
		t.Fatal(err)
	}
	libPath := filepath.Join(tmp, "library")
	if !m.importFile(srcFile, "Fight.Club.1999.1080p.BluRay.mkv", "movie", libPath) {
		t.Fatal("expected import to succeed")
	}
	if _, err := os.Stat(srcFile); err != nil {
		t.Fatal("source should remain after hardlink")
	}
	dest := filepath.Join(libPath, "Movies", "Fight Club (1999)", "Fight Club.1999.1080p.BluRay.mkv")
	if _, err := os.Stat(dest); err != nil {
		t.Fatal("dest missing:", dest)
	}
}

func TestSampleRejection(t *testing.T) {
	m := NewModule(Config{
		DBPath:         filepath.Join(t.TempDir(), "scanner.db"),
		GRPCAddr:       ":0",
		ImportMode:     "move",
		MinVideoBytes:  -1,
		SampleMaxBytes: 200 * 1024 * 1024,
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(ctx) })

	if reason, reject := m.isSampleFile("Movie.Sample.mkv", 50*1024*1024); !reject {
		t.Fatalf("expected sample reject, reason=%q", reason)
	}
	if _, reject := m.isSampleFile("sample.mkv", 10*1024); !reject {
		t.Fatal("expected sample.mkv reject")
	}
	if _, reject := m.isSampleFile("Fight.Club.1999.1080p.mkv", 50*1024*1024); reject {
		t.Fatal("normal release should not reject")
	}

	m.minVideoBytes = 5 * 1024 * 1024
	if _, reject := m.isSampleFile("Fight.Club.1999.1080p.mkv", 1024); !reject {
		t.Fatal("tiny video should reject")
	}
}

func TestParseEditionAndGroup(t *testing.T) {
	ed, grp := parseEditionAndGroup("Movie.1999.Directors.Cut.1080p-GROUP.mkv")
	if ed == "" {
		t.Error("expected edition")
	}
	if grp != "GROUP" {
		t.Errorf("group=%q", grp)
	}
}

func TestParseProperToken(t *testing.T) {
	if got := parseProperToken("Show.S01E01.PROPER.1080p.mkv"); got != "Proper" {
		t.Errorf("want Proper, got %q", got)
	}
	if got := parseProperToken("Show.S01E01.REPACK.1080p.mkv"); got != "Repack" {
		t.Errorf("want Repack, got %q", got)
	}
	if got := parseProperToken("Show.S01E01.1080p.mkv"); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestScanLibraryRootsInPlace(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "library")
	movieDir := filepath.Join(lib, "Movies", "Fight Club (1999)")
	if err := os.MkdirAll(movieDir, 0700); err != nil {
		t.Fatal(err)
	}
	moviePath := filepath.Join(movieDir, "Fight.Club.1999.1080p.mkv")
	if err := os.WriteFile(moviePath, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}

	m := NewModule(Config{
		DBPath:        filepath.Join(tmp, "scanner.db"),
		GRPCAddr:      ":0",
		LibraryRoot:   lib,
		ImportMode:    "move",
		MinVideoBytes: -1,
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(ctx) })

	resp, err := m.ScanLibraryRoots(ctx, &scannerv1.ScanLibraryRootsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesImported < 1 {
		t.Fatalf("expected import, got %+v", resp)
	}
	if _, err := os.Stat(moviePath); err != nil {
		t.Fatal("library file must remain in place")
	}

	resp2, err := m.ScanLibraryRoots(ctx, &scannerv1.ScanLibraryRootsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.FilesImported != 0 || resp2.FilesSkipped < 1 {
		t.Fatalf("second scan should skip: %+v", resp2)
	}
}
