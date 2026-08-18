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

func TestImportTVUsesTVLibraryRoot(t *testing.T) {
	m := newTestModule(t)
	tmp := t.TempDir()
	m.tvLibraryRoot = filepath.Join(tmp, "shows")
	srcFile := filepath.Join(tmp, "Dragon.Tales.S01E01.mkv")
	if err := os.WriteFile(srcFile, []byte("fake tv"), 0644); err != nil {
		t.Fatal(err)
	}
	movieLib := filepath.Join(tmp, "movies")
	if !m.importFile(srcFile, "Dragon.Tales.S01E01.mkv", "both", movieLib) {
		t.Fatal("expected TV import to succeed")
	}
	want := filepath.Join(tmp, "shows", "Dragon Tales", "Season 01", "Dragon Tales.S01E01.mkv")
	if _, err := os.Stat(want); os.IsNotExist(err) {
		t.Fatal("TV file should land in SCANNER_TV_LIBRARY_ROOT, missing:", want)
	}
	nested := filepath.Join(movieLib, "TV")
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Fatal("must not nest TV under the movie library")
	}
}

func TestImportLeadingEpisodeUsesTVLibraryRoot(t *testing.T) {
	m := newTestModule(t)
	tmp := t.TempDir()
	m.tvLibraryRoot = filepath.Join(tmp, "shows")
	srcDir := filepath.Join(tmp, "The New Adventures of Winnie the Pooh")
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(srcDir, "E10 How Much Is That Rabbit In The Window.mkv")
	if err := os.WriteFile(srcFile, []byte("fake tv"), 0644); err != nil {
		t.Fatal(err)
	}
	movieLib := filepath.Join(tmp, "movies")
	if !m.importFile(srcFile, filepath.Base(srcFile), "both", movieLib) {
		t.Fatal("expected leading-episode import to succeed")
	}
	matches, err := filepath.Glob(filepath.Join(tmp, "shows", "The New Adventures of Winnie the Pooh", "*", "*E10*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("expected Pooh episode under TV library, not movies/Other")
	}
	if _, err := os.Stat(filepath.Join(movieLib, "Other")); !os.IsNotExist(err) {
		t.Fatal("must not dump leading-episode TV into movies/Other")
	}
}

func TestImportMovieRootDoesNotDoubleNest(t *testing.T) {
	m := newTestModule(t)
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "Totoro.1988.mkv")
	if err := os.WriteFile(srcFile, []byte("fake movie"), 0644); err != nil {
		t.Fatal(err)
	}
	movieLib := filepath.Join(tmp, "movies")
	if !m.importFile(srcFile, "Totoro.1988.mkv", "movie", movieLib) {
		t.Fatal("expected movie import")
	}
	want := filepath.Join(movieLib, "Totoro (1988)", "Totoro.1988.mkv")
	if _, err := os.Stat(want); os.IsNotExist(err) {
		t.Fatal("expected dest without extra Movies/ folder:", want)
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

func TestImportPathRelativeUnderCwdPartials(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	watch := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	cwd := filepath.Join(tmp, "mvp")
	relDir := filepath.Join("partials", "ep_tv_253", "pending_x")
	savePath := filepath.Join(cwd, relDir)
	if err := os.MkdirAll(watch, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(savePath, 0755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(savePath, "Star.Trek.S03E24.1080p.mkv")
	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: watch, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: filepath.Join(relDir, "Star.Trek.S03E24.1080p.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesImported != 1 {
		t.Fatalf("imported=%d want 1 (cwd/partials must be an import root)", resp.FilesImported)
	}
}

func TestImportPathRelativeUnderWatchDir(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	relDir := filepath.Join("partials", "mv_550", "btih_aa")
	savePath := filepath.Join(srcDir, relDir)
	if err := os.MkdirAll(savePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(savePath, "Test.Movie.2020.1080p.mkv"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}

	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: filepath.Join(relDir, "Test.Movie.2020.1080p.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesImported != 1 {
		t.Fatalf("imported=%d want 1", resp.FilesImported)
	}
}

func TestImportPath(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	savePath := filepath.Join(srcDir, "Test.Movie.2020.1080p")
	if err := os.MkdirAll(savePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(savePath, "Test.Movie.2020.1080p.mkv"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}

	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: savePath})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesFound != 1 {
		t.Errorf("expected 1 found, got %d", resp.FilesFound)
	}
	if resp.FilesImported != 1 {
		t.Errorf("expected 1 imported, got %d", resp.FilesImported)
	}

	dest := filepath.Join(libDir, "Movies", "Test Movie (2020)", "Test Movie.2020.1080p.mkv")
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		t.Fatal("imported file not found at destination:", dest)
	}
}

func TestImportPathFileDoesNotScanSiblings(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(srcDir, "Wanted.Movie.2021.1080p.mkv")
	other := filepath.Join(srcDir, "Other.Movie.2019.1080p.mkv")
	if err := os.WriteFile(want, []byte("wanted-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("other-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}

	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: want})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesFound != 1 || resp.FilesImported != 1 {
		t.Fatalf("found=%d imported=%d want 1/1", resp.FilesFound, resp.FilesImported)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("sibling should not be imported/moved: %v", err)
	}
}

func TestSkipImportWhenDestAlreadyPresent(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "Fight.Club.1999.1080p.mkv")
	payload := []byte("same-payload")
	if err := os.WriteFile(src, payload, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}
	parsed := parseFileName(filepath.Base(src))
	_, dest := m.resolveImportPaths(parsed, libDir, src)
	if dest == "" {
		t.Fatal("expected dest path")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, payload, 0644); err != nil {
		t.Fatal(err)
	}

	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: src})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesImported != 1 {
		t.Fatalf("imported=%d want 1", resp.FilesImported)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source should remain when dest already matches: %v", err)
	}
	if !m.isAlreadyImported(src) {
		t.Fatal("expected original path recorded as imported")
	}
}

func TestImportPathSkipsNonMediaFile(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(srcDir, "RARBG.txt")
	if err := os.WriteFile(junk, []byte("rarbg"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{Path: srcDir, LibraryPath: libDir}); err != nil {
		t.Fatal(err)
	}
	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: junk})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FilesImported != 0 {
		t.Fatalf("imported junk file: %+v", resp)
	}
}

func TestImportPathOutsideWatchDir(t *testing.T) {
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
	if !strings.Contains(err.Error(), "not under any registered watch directory") {
		t.Fatalf("unexpected error: %v", err)
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
		{"Mister.Rogers.Neighborhood.S01.E001.SDTV.mkv", "Mister Rogers Neighborhood", 1, 1, "tv"},
		{"Mister Rogers Neighborhood S03 480p WEBRIP - 005 [480p.WEBRIP].mp4", "Mister Rogers Neighborhood", 3, 5, "tv"},
		{"E10 How Much Is That Rabbit In The Window.mkv", "How Much Is That Rabbit In The Window", 0, 10, "tv"},
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
	if reason, reject := m.isSampleFile("Breaking Bad - S04E01 Extra - Inside Breaking Bad.m4v", 22*1024*1024); !reject {
		t.Fatalf("expected extra reject, reason=%q", reason)
	}
	if reason, reject := m.isSampleFile("Breaking Bad - S04E01 Extended or Alternate Scene - Skyler Gets Her Purse.m4v", 2*1024*1024); !reject {
		t.Fatalf("expected alternate-scene reject, reason=%q", reason)
	}
	if _, reject := m.isSampleFile("Fight.Club.1999.Extended.Cut.1080p.mkv", 50*1024*1024); reject {
		t.Fatal("extended cut edition should not reject as extra")
	}

	m.minVideoBytes = 5 * 1024 * 1024
	if _, reject := m.isSampleFile("Fight.Club.1999.1080p.mkv", 1024); !reject {
		t.Fatal("tiny video should reject")
	}
}

func TestScanSkipsExtrasDir(t *testing.T) {
	m := newTestModule(t)
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "downloads")
	libDir := filepath.Join(tmp, "library")
	extras := filepath.Join(srcDir, "Show S01", "Extras")
	if err := os.MkdirAll(extras, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "Show S01", "Show.S01E01.mkv"), []byte("episode-bytes-here"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extras, "Show - S01E01 Extra - Inside.mkv"), []byte("tiny-extra"), 0644); err != nil {
		t.Fatal(err)
	}

	found, imported, _ := m.scanDirectory(srcDir, "tv", libDir)
	if found != 1 {
		t.Fatalf("found=%d want 1 (extras dir skipped)", found)
	}
	if imported != 1 {
		t.Fatalf("imported=%d want 1", imported)
	}
}

func TestKeepLargerLibraryFile(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dest.mkv")
	src := filepath.Join(t.TempDir(), "src.mkv")
	if err := os.WriteFile(dest, []byte("larger-library-file-contents"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("small"), 0644); err != nil {
		t.Fatal(err)
	}
	if !destKeepExisting(dest, src) {
		t.Fatal("should keep larger dest")
	}
	if destKeepExisting(src, dest) {
		t.Fatal("smaller dest should not block larger src")
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

func TestScanLibraryRootsLeafMoviesDir(t *testing.T) {
	tmp := t.TempDir()
	movies := filepath.Join(tmp, "movies")
	movieDir := filepath.Join(movies, "Dune (2021)")
	if err := os.MkdirAll(movieDir, 0700); err != nil {
		t.Fatal(err)
	}
	moviePath := filepath.Join(movieDir, "Dune (2021) [Unknown].avi")
	if err := os.WriteFile(moviePath, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	extras := filepath.Join(movies, "Fight Club (1999)", "extras")
	if err := os.MkdirAll(extras, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extras, "Fight Club audio track 2 ch.mp4"), []byte("extra"), 0600); err != nil {
		t.Fatal(err)
	}

	shows := filepath.Join(tmp, "shows")
	epDir := filepath.Join(shows, "When Calls the Heart (2014)", "Season 01")
	if err := os.MkdirAll(epDir, 0700); err != nil {
		t.Fatal(err)
	}
	epPath := filepath.Join(epDir, "When Calls the Heart (2014) - S01E01 - Lost and Found.mkv")
	if err := os.WriteFile(epPath, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}

	m := NewModule(Config{
		DBPath:        filepath.Join(tmp, "scanner.db"),
		GRPCAddr:      ":0",
		LibraryRoot:   movies,
		TVLibraryRoot: shows,
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
	if resp.FilesImported < 2 {
		t.Fatalf("expected movie+episode import, got %+v", resp)
	}
	var title string
	var year, season, episode int
	m.mu.RLock()
	err = m.db.QueryRow(`SELECT title, year, season_number, episode_number FROM imported_files WHERE media_type = 'tv' LIMIT 1`).Scan(&title, &year, &season, &episode)
	m.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if title != "When Calls the Heart" || year != 2014 || season != 1 || episode != 1 {
		t.Fatalf("tv import title=%q year=%d S%dE%d", title, year, season, episode)
	}
}

func TestParseFileNameParenYearAndTmdb(t *testing.T) {
	p := parseFileName("The Princess Diaries (2001) [tmdbid-9880] - WEBDL.mkv")
	if p.MediaType != "movie" || p.Title != "The Princess Diaries" || p.Year != 2001 || p.TMDBID != 9880 {
		t.Fatalf("got %+v", p)
	}
	p = parseFileName("Dune (2021) [Unknown].avi")
	if p.MediaType != "movie" || p.Title != "Dune" || p.Year != 2021 {
		t.Fatalf("got %+v", p)
	}
	p = parseFileName("Spider-Man.2.2004.1080p.BluRay.DDP5.1.x265.10bit-GalaxyRG265.mkv")
	if p.MediaType != "movie" || p.Year != 2004 {
		t.Fatalf("x265 must not parse as 1x265 episode, got %+v", p)
	}
	p = parseFileName("Spider-Man- Brand New Day 2026.1080p.HQ Pre.Multi.AAC 2.0.x264.mkv")
	if p.MediaType != "movie" || p.Year != 2026 {
		t.Fatalf("x264 must not parse as 0x264 episode, got %+v", p)
	}
}

func TestParseFileNameTVParenYear(t *testing.T) {
	p := parseFileName("When Calls the Heart (2014) - S01E01 - Lost and Found.mkv")
	if p.MediaType != "tv" || p.Title != "When Calls the Heart" || p.Year != 2014 || p.Season != 1 || p.Episode != 1 {
		t.Fatalf("got %+v", p)
	}
	p = parseFileName("Breaking Bad (2008) - S02E09 - 4 Days Out [HDTV].avi")
	if p.MediaType != "tv" || p.Title != "Breaking Bad" || p.Year != 2008 || p.Season != 2 || p.Episode != 9 {
		t.Fatalf("got %+v", p)
	}
}
