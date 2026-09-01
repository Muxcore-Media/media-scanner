package internal

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSettingsImportAndFilters(t *testing.T) {
	m := NewModule(Config{ImportMode: "move", MinVideoBytes: -1, SafetyRescan: -1})
	defs := m.Settings()
	if len(defs) < 5 {
		t.Fatalf("expected settings, got %d", len(defs))
	}
	if err := m.UpdateSetting("import_mode", "copy"); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	mode := m.importMode
	m.mu.RUnlock()
	if mode != "copy" {
		t.Fatalf("import_mode=%q", mode)
	}
	if err := m.UpdateSetting("sample_max_bytes", "1048576"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("min_video_bytes", "0"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("safety_rescan", "1m"); err != nil {
		t.Fatal(err)
	}
	if got := m.getSafetyRescan(); got.String() != "1m0s" {
		t.Fatalf("safety_rescan=%v", got)
	}
	if err := m.UpdateSetting("library_root", "/tmp/lib"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("import_mode", "nope"); err != nil {
		t.Fatal(err) // normalize falls back to hardlink
	}
	m.mu.RLock()
	mode = m.importMode
	m.mu.RUnlock()
	if mode != "hardlink" {
		t.Fatalf("unknown mode should normalize to hardlink, got %q", mode)
	}
	if err := m.UpdateSetting("unknown", "x"); err == nil {
		t.Fatal("expected unknown setting error")
	}
}

func TestSettingsPersistAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "scanner.db")
	m := NewModule(Config{
		DBPath:        dbPath,
		GRPCAddr:      ":0",
		ImportMode:    "move",
		MinVideoBytes: -1,
		SafetyRescan:  -1,
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("music_library_root", "/data/music"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("use_mesh_storage", "true"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("tv_library_root", "/data/tv"); err != nil {
		t.Fatal(err)
	}
	_ = m.Stop(ctx)

	m2 := NewModule(Config{DBPath: dbPath, GRPCAddr: ":0", MinVideoBytes: -1, SafetyRescan: -1})
	if err := m2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m2.Stop(ctx) })
	m2.mu.RLock()
	music := m2.musicLibraryRoot
	tv := m2.tvLibraryRoot
	mesh := m2.useMeshStorage
	m2.mu.RUnlock()
	if music != "/data/music" || tv != "/data/tv" || !mesh {
		t.Fatalf("persisted settings lost: music=%q tv=%q mesh=%v", music, tv, mesh)
	}
}
