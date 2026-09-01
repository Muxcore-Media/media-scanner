package internal

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func (m *Module) Settings() []contracts.SettingDef {
	return m.settingsDefs()
}

func (m *Module) UpdateSetting(key, value string) error {
	return m.updateSetting(key, value)
}

func (m *Module) settingsDefs() []contracts.SettingDef {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return []contracts.SettingDef{
		{
			Key:         "import_mode",
			Label:       "Import Mode",
			Type:        contracts.SettingTypeString,
			Value:       m.importMode,
			Description: "hardlink | copy | move (SCANNER_IMPORT_MODE)",
			Group:       "Import",
		},
		{
			Key:         "library_root",
			Label:       "Library Root",
			Type:        contracts.SettingTypeString,
			Value:       m.libraryRoot,
			Description: "Movie library root or parent that contains Movies/movies (SCANNER_LIBRARY_ROOT)",
			Group:       "Paths",
		},
		{
			Key:         "tv_library_root",
			Label:       "TV Library Root",
			Type:        contracts.SettingTypeString,
			Value:       m.tvLibraryRoot,
			Description: "TV library root or parent that contains TV/shows (SCANNER_TV_LIBRARY_ROOT)",
			Group:       "Paths",
		},
		{
			Key:         "music_library_root",
			Label:       "Music Library Root",
			Type:        contracts.SettingTypeString,
			Value:       m.musicLibraryRoot,
			Description: "Music library root or parent that contains Music/music (SCANNER_MUSIC_LIBRARY_ROOT)",
			Group:       "Paths",
		},
		{
			Key:         "use_mesh_storage",
			Label:       "Use Mesh Storage",
			Type:        contracts.SettingTypeBool,
			Value:       strconv.FormatBool(m.useMeshStorage),
			Description: "Call core storage.Put when no local dest exists (SCANNER_USE_MESH_STORAGE)",
			Group:       "Import",
		},
		{
			Key:         "sample_max_bytes",
			Label:       "Sample Max Bytes",
			Type:        contracts.SettingTypeString,
			Value:       strconv.FormatInt(m.sampleMaxBytes, 10),
			Description: "Skip files matching sample naming under this size (SCANNER_SAMPLE_MAX_BYTES)",
			Group:       "Filters",
		},
		{
			Key:         "min_video_bytes",
			Label:       "Min Video Bytes",
			Type:        contracts.SettingTypeString,
			Value:       strconv.FormatInt(m.minVideoBytes, 10),
			Description: "Skip videos smaller than this (0 disables; SCANNER_MIN_VIDEO_BYTES)",
			Group:       "Filters",
		},
		{
			Key:         "safety_rescan",
			Label:       "Safety Rescan Interval",
			Type:        contracts.SettingTypeString,
			Value:       m.safetyRescan.String(),
			Description: "Periodic full rescan (0 disables; Go duration; SCANNER_SAFETY_RESCAN)",
			Group:       "Watcher",
		},
	}
}

func (m *Module) updateSetting(key, value string) error {
	value = strings.TrimSpace(value)
	switch key {
	case "import_mode", "SCANNER_IMPORT_MODE":
		mode := normalizeImportMode(value)
		m.mu.Lock()
		m.importMode = mode
		m.mu.Unlock()
		m.persistSetting(key, mode)
		return nil
	case "library_root", "SCANNER_LIBRARY_ROOT":
		if value == "" {
			return fmt.Errorf("library_root must not be empty")
		}
		m.mu.Lock()
		m.libraryRoot = value
		m.mu.Unlock()
		m.persistSetting(key, value)
		m.refreshEmptyWatchDirDests()
		return nil
	case "tv_library_root", "SCANNER_TV_LIBRARY_ROOT":
		m.mu.Lock()
		m.tvLibraryRoot = value
		m.mu.Unlock()
		m.persistSetting(key, value)
		m.refreshEmptyWatchDirDests()
		return nil
	case "music_library_root", "SCANNER_MUSIC_LIBRARY_ROOT":
		m.mu.Lock()
		m.musicLibraryRoot = value
		m.mu.Unlock()
		m.persistSetting(key, value)
		m.refreshEmptyWatchDirDests()
		return nil
	case "use_mesh_storage", "SCANNER_USE_MESH_STORAGE":
		enabled := parseBoolSetting(value)
		m.mu.Lock()
		m.useMeshStorage = enabled
		m.mu.Unlock()
		m.persistSetting(key, strconv.FormatBool(enabled))
		return nil
	case "sample_max_bytes", "SCANNER_SAMPLE_MAX_BYTES":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid sample_max_bytes %q", value)
		}
		m.mu.Lock()
		m.sampleMaxBytes = n
		m.mu.Unlock()
		m.persistSetting(key, value)
		return nil
	case "min_video_bytes", "SCANNER_MIN_VIDEO_BYTES":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid min_video_bytes %q", value)
		}
		m.mu.Lock()
		m.minVideoBytes = n
		m.mu.Unlock()
		m.persistSetting(key, value)
		return nil
	case "safety_rescan", "SCANNER_SAFETY_RESCAN":
		var d time.Duration
		if value == "0" {
			d = 0
		} else {
			parsed, err := time.ParseDuration(value)
			if err != nil || parsed < 0 {
				return fmt.Errorf("invalid safety_rescan %q (use Go duration or 0)", value)
			}
			d = parsed
		}
		m.mu.Lock()
		m.safetyRescan = d
		m.mu.Unlock()
		m.persistSetting(key, value)
		return nil
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}

func parseBoolSetting(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (m *Module) persistSetting(key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return
	}
	_, _ = m.db.ExecContext(context.Background(), `INSERT INTO settings_kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
}

func (m *Module) loadPersistedSettings(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return
	}
	rows, err := m.db.QueryContext(ctx, `SELECT key, value FROM settings_kv`)
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			continue
		}
		switch k {
		case "import_mode", "SCANNER_IMPORT_MODE":
			m.importMode = normalizeImportMode(v)
		case "library_root", "SCANNER_LIBRARY_ROOT":
			if v != "" {
				m.libraryRoot = v
			}
		case "tv_library_root", "SCANNER_TV_LIBRARY_ROOT":
			m.tvLibraryRoot = v
		case "music_library_root", "SCANNER_MUSIC_LIBRARY_ROOT":
			m.musicLibraryRoot = v
		case "use_mesh_storage", "SCANNER_USE_MESH_STORAGE":
			m.useMeshStorage = parseBoolSetting(v)
		case "sample_max_bytes", "SCANNER_SAMPLE_MAX_BYTES":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				m.sampleMaxBytes = n
			}
		case "min_video_bytes", "SCANNER_MIN_VIDEO_BYTES":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				m.minVideoBytes = n
			}
		case "safety_rescan", "SCANNER_SAFETY_RESCAN":
			if v == "0" {
				m.safetyRescan = 0
			} else if d, err := time.ParseDuration(v); err == nil && d >= 0 {
				m.safetyRescan = d
			}
		}
	}
}

func (m *Module) refreshEmptyWatchDirDests() {
	m.mu.Lock()
	db := m.db
	libPath := m.libraryRoot
	tvPath := m.tvLibraryRoot
	m.mu.Unlock()
	if db == nil {
		return
	}
	_, _ = db.Exec(`UPDATE watch_dirs SET library_path = CASE WHEN IFNULL(library_path,'') = '' THEN ? ELSE library_path END, tv_library_path = CASE WHEN IFNULL(tv_library_path,'') = '' THEN ? ELSE tv_library_path END, media_type = CASE WHEN IFNULL(media_type,'') = '' THEN 'both' ELSE media_type END WHERE enabled = 1`,
		libPath, tvPath)
}

func (m *Module) getSafetyRescan() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.safetyRescan
}
