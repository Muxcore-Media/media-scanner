package internal

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/moduletest"
)

func openUpgradeModule(t *testing.T, dbPath string) *Module {
	t.Helper()
	m := NewModule(Config{ID: "upgrade-test", DBPath: dbPath, GRPCAddr: "127.0.0.1:0", LibraryRoot: t.TempDir()})
	if err := m.Init(context.Background()); err != nil {
		t.Fatalf("Init(%s): %v", dbPath, err)
	}
	return m
}

func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUpgradeFromSnapshots(t *testing.T) {
	for _, tag := range []string{"v0.1.9"} {
		t.Run(tag, func(t *testing.T) {
			ctx := context.Background()

			// Fresh schema from current code.
			fm := openUpgradeModule(t, filepath.Join(t.TempDir(), "fresh.db"))
			fresh := moduletest.Schema(t, fm.db)
			if err := fm.Stop(ctx); err != nil {
				t.Fatal(err)
			}

			path := moduletest.CopyFixture(t, filepath.Join("testdata", "upgrade", tag+".db"))

			// Open twice: first run migrates, second proves idempotent startup.
			m := openUpgradeModule(t, path)
			if err := m.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			m = openUpgradeModule(t, path)
			defer func() { _ = m.Stop(ctx) }()

			moduletest.RequireSchemaSuperset(t, moduletest.Schema(t, m.db), fresh)

			// Seeded rows read back through the current API.
			wd, err := m.ListWatchDirs(ctx, &scannerv1.ListWatchDirsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if len(wd.Dirs) != 3 {
				t.Fatalf("watch dirs = %d, want 3", len(wd.Dirs))
			}
			byID := map[string]*scannerv1.WatchDir{}
			for _, d := range wd.Dirs {
				byID[d.Id] = d
			}
			if d := byID["wd_1"]; d == nil || d.Path != "/home/alice/Downloads" || d.MediaType != "both" ||
				d.LibraryPath != "/data/media/movies" || !d.Enabled || d.CreatedAt != "2025-01-02T03:04:05Z" {
				t.Errorf("wd_1 = %+v", d)
			}
			if d := byID["wd_2"]; d == nil || d.Path != "/mnt/torrents/tv" || d.MediaType != "tv" || d.Enabled {
				t.Errorf("wd_2 = %+v", d)
			}
			if d := byID["wd_3"]; d == nil || d.Path != "/srv/bob/music-drop" || d.MediaType != "music" || d.LibraryPath != "" {
				t.Errorf("wd_3 = %+v", d)
			}
			// New columns default to ''.
			for id, d := range byID {
				if d.TvLibraryPath != "" || d.MusicLibraryPath != "" {
					t.Errorf("%s new columns not defaulted: tv=%q music=%q", id, d.TvLibraryPath, d.MusicLibraryPath)
				}
			}
			var nullCount int
			if err := m.db.QueryRow(`SELECT COUNT(*) FROM watch_dirs WHERE tv_library_path IS NULL OR music_library_path IS NULL`).Scan(&nullCount); err != nil || nullCount != 0 {
				t.Errorf("NULL new columns: n=%d err=%v", nullCount, err)
			}

			imp, err := m.ListImported(ctx, &scannerv1.ListImportedRequest{PageSize: 100})
			if err != nil {
				t.Fatal(err)
			}
			if imp.Total != 3 || len(imp.Files) != 3 {
				t.Fatalf("imported total=%d files=%d, want 3", imp.Total, len(imp.Files))
			}
			files := map[string]*scannerv1.ImportedFile{}
			for _, f := range imp.Files {
				files[f.Id] = f
			}
			if f := files["if_1"]; f == nil || f.OriginalPath != "/home/alice/Downloads/The.Matrix.1999.1080p.mkv" ||
				f.DestinationPath != "/data/media/movies/The Matrix (1999)/The Matrix (1999).mkv" ||
				f.FileName != "The Matrix (1999).mkv" || f.MediaType != "movie" || f.Title != "The Matrix" ||
				f.Year != 1999 || f.Quality != "1080p" || f.TmdbId != 603 ||
				f.ImportedAt != "2025-01-02T10:00:00Z" || f.Status != "imported" {
				t.Errorf("if_1 = %+v", f)
			}
			if f := files["if_2"]; f == nil || f.MediaType != "tv" || f.Title != "Show" || f.SeasonNumber != 2 ||
				f.EpisodeNumber != 5 || f.Quality != "720p" || f.TmdbId != 1399 {
				t.Errorf("if_2 = %+v", f)
			}
			if f := files["if_3"]; f == nil || f.Title != "Family Holiday" || f.Status != "failed" || f.Year != 0 || f.TmdbId != 0 {
				t.Errorf("if_3 = %+v", f)
			}
			tv, err := m.ListImported(ctx, &scannerv1.ListImportedRequest{MediaType: "tv"})
			if err != nil || tv.Total != 1 {
				t.Errorf("tv filter total=%v err=%v", tv.GetTotal(), err)
			}

			var found, imported, skipped int
			var status string
			var completed sql.NullString
			if err := m.db.QueryRow(`SELECT files_found, files_imported, files_skipped, status FROM scan_log WHERE id='sl_1'`).Scan(&found, &imported, &skipped, &status); err != nil ||
				found != 10 || imported != 7 || skipped != 3 || status != "completed" {
				t.Errorf("sl_1 = %d/%d/%d %q err=%v", found, imported, skipped, status, err)
			}
			if err := m.db.QueryRow(`SELECT completed_at, status FROM scan_log WHERE id='sl_2'`).Scan(&completed, &status); err != nil ||
				completed.Valid || status != "running" {
				t.Errorf("sl_2 completed=%v status=%q err=%v", completed, status, err)
			}

			// Upgraded DB accepts writes through the current API.
			if _, err := m.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{
				Path: "/new/dir", MediaType: "tv", TvLibraryPath: "/data/tv", MusicLibraryPath: "/data/music",
			}); err != nil {
				t.Errorf("AddWatchDir after upgrade: %v", err)
			}

			moduletest.RequireIntegrity(t, m.db)
			moduletest.RequireIntegrity(t, rawDB(t, path))
		})
	}
}
