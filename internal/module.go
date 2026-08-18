package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
	scannerv1 "github.com/Muxcore-Media/media-scanner/proto/scannerv1"
	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/core/sdk/go/client"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	"github.com/fsnotify/fsnotify"
	_ "modernc.org/sqlite"
)

type Module struct {
	scannerv1.UnimplementedScannerServiceServer

	mu sync.RWMutex
	db *sql.DB
	mc *client.Client

	id              string
	dbPath          string
	grpcAddr        string
	libraryRoot     string
	tvLibraryRoot   string
	defaultWatchDir string
	importMode      string
	sampleMaxBytes  int64
	minVideoBytes   int64
	grpcSrv         *grpc.Server
	grpcLis         net.Listener

	watcher       *fsnotify.Watcher
	watched       map[string]struct{}
	watchCancel   context.CancelFunc
	debounceMu    sync.Mutex
	debounceTimer *time.Timer
	debounceWait  time.Duration
	safetyRescan  time.Duration
	initialDelay  time.Duration
	scanMu        sync.Mutex

	putFailMu    sync.Mutex
	putFailUntil map[string]time.Time
}

type Config struct {
	ID               string
	DBPath           string
	GRPCAddr         string
	LibraryRoot      string
	TVLibraryRoot    string
	DefaultWatchDir  string
	ImportMode       string
	SampleMaxBytes   int64
	MinVideoBytes    int64
	DebounceWait     time.Duration
	SafetyRescan     time.Duration
	InitialScanDelay time.Duration
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "media-scanner"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "/var/lib/media-scanner/scanner.db"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = ":9470"
	}
	if cfg.LibraryRoot == "" {
		cfg.LibraryRoot = "/data/media"
	}
	if cfg.SampleMaxBytes <= 0 {
		cfg.SampleMaxBytes = 200 * 1024 * 1024
	}
	minVideo := int64(5 * 1024 * 1024)
	if cfg.MinVideoBytes < 0 {
		minVideo = 0
	} else if cfg.MinVideoBytes > 0 {
		minVideo = cfg.MinVideoBytes
	}
	if v := os.Getenv("SCANNER_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("SCANNER_GRPC_ADDR"); v != "" {
		cfg.GRPCAddr = v
	}
	if v := os.Getenv("SCANNER_LIBRARY_ROOT"); v != "" {
		cfg.LibraryRoot = v
	}
	if v := os.Getenv("SCANNER_TV_LIBRARY_ROOT"); v != "" {
		cfg.TVLibraryRoot = v
	}
	if v := os.Getenv("SCANNER_DEFAULT_WATCH_DIR"); v != "" {
		cfg.DefaultWatchDir = v
	}
	if v := os.Getenv("SCANNER_IMPORT_MODE"); v != "" {
		cfg.ImportMode = v
	}
	if v := os.Getenv("SCANNER_SAMPLE_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.SampleMaxBytes = n
		}
	}
	if v := os.Getenv("SCANNER_MIN_VIDEO_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			if n < 0 {
				minVideo = 0
			} else {
				minVideo = n
			}
		}
	}
	debounceWait := cfg.DebounceWait
	if debounceWait <= 0 {
		debounceWait = 5 * time.Second
	}
	initialDelay := 5 * time.Second
	if cfg.InitialScanDelay < 0 {
		initialDelay = 0
	} else if cfg.InitialScanDelay > 0 {
		initialDelay = cfg.InitialScanDelay
	}
	safetyRescan := 15 * time.Minute
	if cfg.SafetyRescan < 0 {
		safetyRescan = 0
	} else if cfg.SafetyRescan > 0 {
		safetyRescan = cfg.SafetyRescan
	}
	if v := os.Getenv("SCANNER_SAFETY_RESCAN"); v != "" {
		if v == "0" {
			safetyRescan = 0
		} else if d, err := time.ParseDuration(v); err == nil {
			safetyRescan = d
		}
	}
	return &Module{
		id:              cfg.ID,
		dbPath:          cfg.DBPath,
		grpcAddr:        cfg.GRPCAddr,
		libraryRoot:     cfg.LibraryRoot,
		tvLibraryRoot:   cfg.TVLibraryRoot,
		defaultWatchDir: cfg.DefaultWatchDir,
		importMode:      normalizeImportMode(cfg.ImportMode),
		sampleMaxBytes:  cfg.SampleMaxBytes,
		minVideoBytes:   minVideo,
		debounceWait:    debounceWait,
		safetyRescan:    safetyRescan,
		initialDelay:    initialDelay,
	}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:             m.id,
		Name:           "Media Scanner",
		Version:        "0.1.20",
		Roles:          []string{"scanner"},
		Description:    "Scans download directories, identifies media files, and imports them into the library",
		Author:         "MuxCore",
		Capabilities:   []string{"media.scanner", "settings"},
		MinCoreVersion: "0.4.0",
		HTTPAddr:       m.grpcAddr,
	}
}

func (m *Module) Init(ctx context.Context) error {
	dir := filepath.Dir(m.dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", m.dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS watch_dirs (
			id              TEXT PRIMARY KEY,
			path            TEXT NOT NULL UNIQUE,
			media_type      TEXT NOT NULL DEFAULT 'both',
			library_path    TEXT NOT NULL DEFAULT '',
			tv_library_path TEXT NOT NULL DEFAULT '',
			enabled         INTEGER DEFAULT 1,
			created_at      TEXT NOT NULL
		)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create watch_dirs table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE watch_dirs ADD COLUMN tv_library_path TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			db.Close()
			return fmt.Errorf("add watch_dirs.tv_library_path: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS imported_files (
			id               TEXT PRIMARY KEY,
			original_path    TEXT NOT NULL UNIQUE,
			destination_path TEXT NOT NULL,
			file_name        TEXT NOT NULL,
			media_type       TEXT DEFAULT '',
			title            TEXT DEFAULT '',
			year             INTEGER DEFAULT 0,
			season_number    INTEGER DEFAULT 0,
			episode_number   INTEGER DEFAULT 0,
			quality          TEXT DEFAULT '',
			tmdb_id          INTEGER DEFAULT 0,
			imported_at      TEXT NOT NULL,
			status           TEXT DEFAULT 'imported'
		)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create imported_files table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS scan_log (
			id             TEXT PRIMARY KEY,
			started_at     TEXT NOT NULL,
			completed_at   TEXT,
			files_found    INTEGER DEFAULT 0,
			files_imported INTEGER DEFAULT 0,
			files_skipped  INTEGER DEFAULT 0,
			status         TEXT DEFAULT 'running'
		)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create scan_log table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_imported_type ON imported_files(media_type, status)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create imported index: %w", err)
	}

	m.mu.Lock()
	m.db = db
	m.mu.Unlock()

	// Auto-register default watch directory if configured.
	if m.defaultWatchDir != "" {
		watchID := fmt.Sprintf("auto_watch_%d", time.Now().UnixNano())
		libPath := m.libraryRoot
		tvPath := m.tvLibraryRoot
		m.db.Exec(`INSERT OR IGNORE INTO watch_dirs (id, path, media_type, library_path, tv_library_path, enabled, created_at) VALUES (?, ?, ?, ?, ?, 1, ?)`,
			watchID, m.defaultWatchDir, "both", libPath, tvPath, time.Now().UTC().Format(time.RFC3339),
		)
		m.db.Exec(`UPDATE watch_dirs SET library_path = CASE WHEN IFNULL(library_path,'') = '' THEN ? ELSE library_path END, tv_library_path = CASE WHEN IFNULL(tv_library_path,'') = '' THEN ? ELSE tv_library_path END, media_type = CASE WHEN IFNULL(media_type,'') = '' THEN 'both' ELSE media_type END WHERE path = ?`,
			libPath, tvPath, m.defaultWatchDir,
		)
		slog.Info("auto-registered watch dir", "path", m.defaultWatchDir, "movies", libPath, "tv", tvPath)
	}

	lis, err := net.Listen("tcp", m.grpcAddr)
	if err != nil {
		db.Close()
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.grpcLis = lis

	slog.Info("media-scanner initialized",
		"db", m.dbPath,
		"grpc", m.grpcAddr,
	)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	m.grpcSrv = grpc.NewServer()
	scannerv1.RegisterScannerServiceServer(m.grpcSrv, m)
	modulesdk.RegisterSettings(m.grpcSrv, m.id, m)

	go func() {
		slog.Info("media-scanner gRPC service started", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(m.grpcLis); err != nil {
			slog.Error("media-scanner gRPC serve error", "error", err)
		}
	}()

	go m.dialCore(context.Background())
	if err := m.startWatcher(); err != nil {
		slog.Error("media-scanner: fsnotify watcher failed", "error", err)
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	m.watchCancel = cancel
	go m.watchLoop(watchCtx)
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	m.stopWatcher()
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	if m.mc != nil {
		m.mc.Close()
	}
	m.mu.Lock()
	if m.db != nil {
		m.db.Close()
		m.db = nil
	}
	m.mu.Unlock()
	slog.Info("media-scanner stopped")
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("not initialized")
	}
	return db.PingContext(ctx)
}

func (m *Module) dialCore(ctx context.Context) {
	meshAddr := os.Getenv("MUXCORE_GRPC_ADDR")
	if meshAddr == "" {
		meshAddr = "localhost:9090"
	}
	insecureMode := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_GRPC_INSECURE") == "true"
	var opts []client.Option
	if insecureMode {
		opts = append(opts, client.WithInsecure())
	}
	c, err := client.Dial(meshAddr, opts...)
	if err != nil {
		slog.Error("media-scanner: dial core", "error", err)
		return
	}
	m.mc = c
	slog.Info("media-scanner: connected to core mesh", "addr", meshAddr)
}

func (m *Module) publish(ctx context.Context, eventType string, payload map[string]interface{}) {
	if m.mc == nil {
		return
	}
	data, _ := json.Marshal(payload)
	if err := m.mc.Events.Publish(ctx, eventType, m.id, data); err != nil {
		slog.Warn("publish event failed", "type", eventType, "error", err)
	}
}

// ── Scan Loop ─────────────────────────────────────────────────

func (m *Module) runScan() {
	if !m.scanMu.TryLock() {
		m.scheduleScan()
		return
	}
	defer m.scanMu.Unlock()

	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return
	}

	dirs := m.collectWatchDirs()
	if len(dirs) == 0 {
		return
	}

	logID := fmt.Sprintf("scan_%d", time.Now().UnixNano())
	startedAt := time.Now().UTC().Format(time.RFC3339)
	db.Exec(`INSERT INTO scan_log (id, started_at, status) VALUES (?, ?, 'running')`, logID, startedAt)

	var totalFound, totalImported, totalSkipped int
	for _, d := range dirs {
		found, imported, skipped := m.scanDirectory(d.path, d.mediaType, d.libPath, d.tvLibPath)
		totalFound += found
		totalImported += imported
		totalSkipped += skipped
	}

	completedAt := time.Now().UTC().Format(time.RFC3339)
	db.Exec(`UPDATE scan_log SET completed_at = ?, files_found = ?, files_imported = ?, files_skipped = ?, status = 'completed' WHERE id = ?`,
		completedAt, totalFound, totalImported, totalSkipped, logID)

	if totalFound > 0 {
		slog.Info("scan complete", "found", totalFound, "imported", totalImported, "skipped", totalSkipped)
	}
}

type watchDirEntry struct {
	path      string
	mediaType string
	libPath   string
	tvLibPath string
}

func (m *Module) collectWatchDirs() []watchDirEntry {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return nil
	}

	rows, err := db.Query(`SELECT path, media_type, library_path, IFNULL(tv_library_path,'') FROM watch_dirs WHERE enabled = 1`)
	if err != nil {
		slog.Error("query watch dirs", "error", err)
		return nil
	}
	defer rows.Close()

	var dirs []watchDirEntry
	for rows.Next() {
		var d watchDirEntry
		if err := rows.Scan(&d.path, &d.mediaType, &d.libPath, &d.tvLibPath); err != nil {
			slog.Error("scan watch dir row", "error", err)
			continue
		}
		if strings.TrimSpace(d.tvLibPath) == "" {
			d.tvLibPath = m.tvLibraryRoot
		}
		dirs = append(dirs, d)
	}
	return dirs
}

func (m *Module) scanDirectory(watchPath, mediaType, libPath, tvLibPath string) (found, imported, skipped int) {
	entries, err := os.ReadDir(watchPath)
	if err != nil {
		slog.Warn("cannot read directory", "path", watchPath, "error", err)
		return 0, 0, 0
	}

	for _, entry := range entries {
		if entry.IsDir() {
			if skipBonusDir(entry.Name()) {
				continue
			}
			subFound, subImported, subSkipped := m.scanDirectory(filepath.Join(watchPath, entry.Name()), mediaType, libPath, tvLibPath)
			found += subFound
			imported += subImported
			skipped += subSkipped
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		fullPath := filepath.Join(watchPath, entry.Name())

		if kind, isFirst := archiveKind(entry.Name()); kind != "" {
			if !isFirst {
				continue
			}
			dest, err := m.maybeExtractArchive(fullPath)
			if err != nil {
				slog.Warn("archive extract failed", "path", fullPath, "error", err)
				continue
			}
			if dest != "" {
				subFound, subImported, subSkipped := m.scanDirectory(dest, mediaType, libPath, tvLibPath)
				found += subFound
				imported += subImported
				skipped += subSkipped
			}
			continue
		}

		if !isMediaExt(ext) {
			continue
		}

		found++

		if m.isAlreadyImported(fullPath) {
			skipped++
			continue
		}

		var size int64
		if info, err := entry.Info(); err == nil {
			size = info.Size()
		}
		if reason, reject := m.isSampleFile(entry.Name(), size); reject {
			slog.Info("skipping sample/junk file", "file", entry.Name(), "reason", reason, "size", size)
			skipped++
			continue
		}

		result := m.importFile(fullPath, entry.Name(), mediaType, libPath, tvLibPath)
		if result {
			imported++
		} else {
			skipped++
		}
	}
	return
}

func isMediaExt(ext string) bool {
	switch ext {
	case ".mkv", ".mp4", ".avi", ".m4v", ".mov", ".wmv", ".ts", ".iso":
		return true
	}
	return false
}

func (m *Module) isAlreadyImported(path string) bool {
	var count int
	m.mu.RLock()
	m.db.QueryRow(`SELECT COUNT(*) FROM imported_files WHERE original_path = ? OR destination_path = ?`, path, path).Scan(&count)
	m.mu.RUnlock()
	return count > 0
}

// ── File Import ────────────────────────────────────────────────

func (m *Module) importFile(fullPath, fileName, mediaType, libPath, tvLibPath string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	if !isMediaExt(ext) {
		slog.Debug("skipping non-media file", "file", fileName)
		return false
	}
	var size int64
	if info, err := os.Stat(fullPath); err == nil {
		size = info.Size()
	}
	if reason, reject := m.isSampleFile(fileName, size); reject {
		slog.Info("skipping sample/junk file", "file", fileName, "reason", reason, "size", size)
		return false
	}
	if pathInBonusDir(fullPath) {
		slog.Info("skipping extra/bonus file", "file", fileName, "path", fullPath)
		return false
	}

	parsed := parseFileName(fileName)
	enrichParsedFromPath(&parsed, fullPath)
	if parsed.MediaType == "other" {
		switch {
		case mediaType == "tv" || looksLikeTVName(fileName) || looksLikeTVName(fullPath):
			parsed.MediaType = "tv"
		case mediaType == "movie" || parsed.Year > 0:
			parsed.MediaType = "movie"
		}
	}
	if isJunkImportTitle(parsed.Title) {
		slog.Info("skipping junk title", "file", fileName, "title", parsed.Title)
		return false
	}
	if parsed.Title == "" || parsed.MediaType == "other" {
		slog.Info("skipping unrecognized media", "file", fileName, "title", parsed.Title)
		return false
	}

	if mediaType != "both" && mediaType != "" && parsed.MediaType != mediaType {
		slog.Debug("media type mismatch", "file", fileName, "detected", parsed.MediaType, "expected", mediaType)
		return false
	}

	storageKey, destPath := m.resolveImportPaths(parsed, libPath, tvLibPath, fullPath)
	if storageKey == "" {
		slog.Debug("unable to build storage key", "file", fileName)
		return false
	}

	if destKeepExisting(destPath, fullPath) {
		slog.Info("skip import; destination already present",
			"src", fileName, "dest", destPath, "storage_key", storageKey)
		m.recordImported(fullPath, destPath, fileName, parsed, parsed.Quality)
		return true
	}
	if keep, reason, existing := shouldKeepExistingEpisode(destPath, fullPath, parsed); keep {
		slog.Info("skip import; equal or better episode already in library",
			"src", fileName, "existing", existing, "reason", reason)
		m.recordImported(fullPath, existing, fileName, parsed, parsed.Quality)
		return true
	}

	usedStorage := false
	destExists := destPath != ""
	if destExists {
		if _, err := os.Stat(destPath); err != nil {
			destExists = false
		}
	}
	// Local library dest: copy/link on disk. Mesh Storage.Put EOFs on large files
	// (vault remuxes) and then falls back anyway — skip the round-trip.
	tryStorage := m.mc != nil && destPath == "" && !destExists && !m.storagePutCooling(storageKey)
	if tryStorage {
		f, err := os.Open(fullPath)
		if err != nil {
			slog.Error("open source file for storage put", "src", fullPath, "error", err)
			return false
		}
		if err := m.mc.Storage.Put(context.Background(), storageKey, f); err != nil {
			f.Close()
			m.markStoragePutFailed(storageKey)
			slog.Warn("storage put failed; falling back to local import",
				"key", storageKey, "error", err, "mode", m.importMode)
		} else {
			f.Close()
			usedStorage = true
			if err := os.Remove(fullPath); err != nil {
				slog.Debug("remove source after storage put", "src", fullPath, "error", err)
			}
		}
	}
	if !usedStorage {
		if destKeepExisting(destPath, fullPath) {
			slog.Info("skip local copy; destination already present",
				"src", fileName, "dest", destPath)
		} else {
			destDir := filepath.Dir(destPath)
			if err := os.MkdirAll(destDir, 0755); err != nil {
				slog.Error("create destination directory", "path", destDir, "error", err)
				return false
			}
			if err := placeFile(fullPath, destPath, m.importMode); err != nil {
				slog.Error("place file", "src", fullPath, "dst", destPath, "mode", m.importMode, "error", err)
				return false
			}
			removeWorseEpisodeCopies(destPath, parsed)
		}
	}

	recordedDest := destPath
	if recordedDest == "" {
		recordedDest = storageKey
	}

	quality := parsed.Quality
	analyzePath := destPath
	if _, err := os.Stat(analyzePath); err != nil {
		analyzePath = fullPath
	}
	if q := m.probeQuality(analyzePath); q != "" {
		quality = q
	}

	m.importSidecarSubtitles(fullPath, destPath, storageKey)
	m.detectEmbeddedSubtitles(analyzePath)

	if m.importMode == "move" {
		if _, err := os.Stat(fullPath); err == nil {
			os.Remove(fullPath)
		}
	}

	m.recordImported(fullPath, recordedDest, fileName, parsed, quality)

	slog.Info("imported file", "src", fileName, "dest", recordedDest, "storage_key", storageKey, "type", parsed.MediaType, "title", parsed.Title, "mode", m.importMode)

	go m.publish(context.Background(), contracts.EventFileImported, map[string]interface{}{
		"original_path":    fullPath,
		"destination_path": recordedDest,
		"storage_key":      storageKey,
		"media_type":       parsed.MediaType,
		"title":            parsed.Title,
		"year":             parsed.Year,
		"season_number":    parsed.Season,
		"episode_number":   parsed.Episode,
		"episode_numbers":  episodeNumbersInt32(parsed.Episodes, parsed.Episode),
		"absolute_number":  parsed.AbsoluteNumber,
		"air_date":         parsed.AirDate,
		"season_pack":      parsed.SeasonPack,
		"quality":          quality,
		"tmdb_id":          parsed.TMDBID,
	})

	return true
}

func destKeepExisting(destPath, srcPath string) bool {
	if destPath == "" || srcPath == "" {
		return false
	}
	di, err := os.Stat(destPath)
	if err != nil || di.IsDir() {
		return false
	}
	si, err := os.Stat(srcPath)
	if err != nil || si.IsDir() {
		return false
	}
	return di.Size() > 0 && di.Size() >= si.Size()
}

var reEpisodeTagInName = regexp.MustCompile(`(?i)S(\d{1,2})E(\d{1,4})`)

func shouldKeepExistingEpisode(destPath, srcPath string, parsed parsedFile) (keep bool, reason, existing string) {
	if parsed.MediaType != "tv" || destPath == "" {
		return false, "", ""
	}
	best := bestExistingEpisodeFile(filepath.Dir(destPath), parsed)
	if best == "" || sameFilePath(best, destPath) || sameFilePath(best, srcPath) {
		return false, "", ""
	}
	if !sourceBeatsEpisodeFile(srcPath, parsed.Quality, best) {
		return true, "existing quality/size wins", best
	}
	return false, "", best
}

func removeWorseEpisodeCopies(keptPath string, parsed parsedFile) {
	if parsed.MediaType != "tv" || keptPath == "" {
		return
	}
	dir := filepath.Dir(keptPath)
	for _, p := range episodeFilesInDir(dir, parsed) {
		if sameFilePath(p, keptPath) {
			continue
		}
		if !sourceBeatsEpisodeFile(keptPath, parsed.Quality, p) {
			continue
		}
		if err := os.Remove(p); err != nil {
			slog.Warn("remove superseded episode copy", "path", p, "kept", keptPath, "error", err)
			continue
		}
		slog.Info("removed superseded episode copy", "path", p, "kept", keptPath)
	}
}

func bestExistingEpisodeFile(dir string, parsed parsedFile) string {
	files := episodeFilesInDir(dir, parsed)
	if len(files) == 0 {
		return ""
	}
	best := files[0]
	for _, p := range files[1:] {
		if sourceBeatsEpisodeFile(p, extractQuality(filepath.Base(p)), best) {
			best = p
		}
	}
	return best
}

func episodeFilesInDir(dir string, parsed parsedFile) []string {
	if dir == "" || dir == "." {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isMediaExt(strings.ToLower(filepath.Ext(name))) {
			continue
		}
		if !fileMatchesEpisode(name, parsed) {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}

func fileMatchesEpisode(name string, parsed parsedFile) bool {
	tags := reEpisodeTagInName.FindAllStringSubmatch(name, -1)
	if len(tags) == 0 {
		return false
	}
	want := map[int]struct{}{}
	eps := parsed.Episodes
	if len(eps) == 0 && parsed.Episode > 0 {
		eps = []int{parsed.Episode}
	}
	if len(eps) == 0 {
		return false
	}
	for _, e := range eps {
		want[e] = struct{}{}
	}
	got := map[int]struct{}{}
	gotSeason := -1
	for _, m := range tags {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		if gotSeason < 0 {
			gotSeason = s
		} else if s != gotSeason {
			return false
		}
		got[e] = struct{}{}
	}
	if gotSeason != parsed.Season {
		return false
	}
	if len(got) != len(want) {
		return false
	}
	for e := range want {
		if _, ok := got[e]; !ok {
			return false
		}
	}
	return true
}

func sourceBeatsEpisodeFile(srcPath, srcQuality, existingPath string) bool {
	if existingPath == "" {
		return true
	}
	qNew := qualityScore(srcQuality)
	if qNew == 0 {
		qNew = qualityScore(extractQuality(filepath.Base(srcPath)))
	}
	qOld := qualityScore(extractQuality(filepath.Base(existingPath)))
	if qNew != qOld {
		return qNew > qOld
	}
	si, err1 := os.Stat(srcPath)
	di, err2 := os.Stat(existingPath)
	if err1 != nil || err2 != nil {
		return false
	}
	return si.Size() > di.Size()
}

func qualityScore(label string) int {
	l := strings.ToLower(label)
	score := 0
	switch {
	case strings.Contains(l, "2160") || strings.Contains(l, "4k") || strings.Contains(l, "uhd"):
		score += 4000
	case strings.Contains(l, "1080"):
		score += 1080
	case strings.Contains(l, "900"):
		score += 900
	case strings.Contains(l, "720"):
		score += 720
	case strings.Contains(l, "576"):
		score += 576
	case strings.Contains(l, "480"):
		score += 480
	}
	switch {
	case strings.Contains(l, "remux"):
		score += 50
	case strings.Contains(l, "bluray") || strings.Contains(l, "blu-ray") || strings.Contains(l, "bdrip") || strings.Contains(l, "brrip"):
		score += 30
	case strings.Contains(l, "web-dl") || strings.Contains(l, "webdl"):
		score += 20
	case strings.Contains(l, "webrip"):
		score += 10
	case strings.Contains(l, "hdtv"):
		score += 5
	}
	return score
}

func sameFilePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func (m *Module) recordImported(fullPath, destPath, fileName string, parsed parsedFile, quality string) {
	if m.isAlreadyImported(fullPath) || (destPath != "" && m.isAlreadyImported(destPath)) {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	importID := fmt.Sprintf("imp_%d", time.Now().UnixNano())
	m.mu.Lock()
	_, _ = m.db.Exec(`INSERT INTO imported_files (id, original_path, destination_path, file_name, media_type, title, year, season_number, episode_number, quality, tmdb_id, imported_at, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'imported')`,
		importID, fullPath, destPath, fileName, parsed.MediaType, parsed.Title, parsed.Year, parsed.Season, parsed.Episode, quality, parsed.TMDBID, now,
	)
	m.mu.Unlock()
}

func (m *Module) storagePutCooling(key string) bool {
	m.putFailMu.Lock()
	defer m.putFailMu.Unlock()
	until, ok := m.putFailUntil[key]
	return ok && time.Now().Before(until)
}

func (m *Module) markStoragePutFailed(key string) {
	m.putFailMu.Lock()
	defer m.putFailMu.Unlock()
	if m.putFailUntil == nil {
		m.putFailUntil = make(map[string]time.Time)
	}
	m.putFailUntil[key] = time.Now().Add(15 * time.Minute)
}

func (m *Module) destRootFor(parsed parsedFile, libPath, tvLibPath string) string {
	if parsed.MediaType == "tv" {
		tv := strings.TrimSpace(tvLibPath)
		if tv == "" {
			m.mu.RLock()
			tv = strings.TrimSpace(m.tvLibraryRoot)
			m.mu.RUnlock()
		}
		if tv == "" && isMovieLibraryRoot(libPath) {
			tv = filepath.Join(filepath.Dir(filepath.Clean(libPath)), "shows")
		}
		if tv != "" {
			return tv
		}
	}
	if strings.TrimSpace(libPath) != "" {
		return libPath
	}
	return m.libraryRoot
}

func isMovieLibraryRoot(root string) bool {
	base := strings.ToLower(filepath.Base(filepath.Clean(root)))
	for _, n := range movieLibraryDirNames {
		if base == strings.ToLower(n) {
			return true
		}
	}
	return false
}

func libraryKindPrefix(root, mediaType string) string {
	base := strings.ToLower(filepath.Base(filepath.Clean(root)))
	switch mediaType {
	case "movie":
		for _, n := range movieLibraryDirNames {
			if base == strings.ToLower(n) {
				return ""
			}
		}
		return "Movies"
	case "tv":
		for _, n := range tvLibraryDirNames {
			if base == strings.ToLower(n) {
				return ""
			}
		}
		return "TV"
	default:
		return "Other"
	}
}

func (m *Module) resolveImportPaths(parsed parsedFile, libPath, tvLibPath, fullPath string) (storageKey, destPath string) {
	storageKey = m.buildStorageKey(parsed)
	destPath = m.buildDestPath(parsed, libPath, tvLibPath)
	tplID := m.namingTemplateForLibPath(m.destRootFor(parsed, libPath, tvLibPath))
	if preview := m.previewRename(fullPath, parsed, tplID); preview != nil && preview.GetNewPath() != "" {
		root := m.destRootFor(parsed, libPath, tvLibPath)
		folder := libraryKindPrefix(root, parsed.MediaType)
		rel := filepath.Clean(preview.GetNewPath())
		if folder == "" {
			storageKey = filepath.ToSlash(filepath.Join("media", rel))
			destPath = filepath.Join(root, rel)
		} else {
			storageKey = filepath.ToSlash(filepath.Join("media", folder, rel))
			destPath = filepath.Join(root, folder, rel)
		}
	}
	return storageKey, destPath
}

func (m *Module) findCapabilityAddr(ctx context.Context, capability string) (string, error) {
	if m.mc == nil {
		return "", fmt.Errorf("not connected to core")
	}
	modules, err := m.mc.Discovery.FindByCapability(ctx, capability)
	if err != nil {
		return "", err
	}
	for _, mod := range modules {
		addr := dialAddrForModule(mod.Id, mod.HttpAddr)
		if addr != "" {
			return addr, nil
		}
	}
	return "", fmt.Errorf("no %s module found", capability)
}

// dialAddrForModule maps discovery HttpAddr to a dial target.
// Bare/wildcard hosts use module ID (compose DNS) unless MUXCORE_MESH_DIAL_LOCAL=true.
func dialAddrForModule(moduleID, httpAddr string) string {
	if httpAddr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(httpAddr)
	if err != nil || port == "" {
		return httpAddr
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return net.JoinHostPort(host, port)
	}
	if os.Getenv("MUXCORE_MESH_DIAL_LOCAL") == "true" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	if moduleID != "" {
		return net.JoinHostPort(moduleID, port)
	}
	return httpAddr
}

func (m *Module) previewRename(fullPath string, parsed parsedFile, templateID string) *renamev1.PreviewResponse {
	ctx := context.Background()
	addr, err := m.findCapabilityAddr(ctx, "media.renamer")
	if err != nil {
		return nil
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil
	}
	defer conn.Close()
	cli := renamev1.NewRenameServiceClient(conn)

	edition, group := parseEditionAndGroup(parsed.FileName)
	req := &renamev1.PreviewRequest{
		FilePath:       fullPath,
		MediaType:      parsed.MediaType,
		Title:          parsed.Title,
		Year:           int32(parsed.Year),
		SeasonNumber:   int32(parsed.Season),
		EpisodeNumber:  int32(parsed.Episode),
		EpisodeNumbers: episodeNumbersInt32(parsed.Episodes, parsed.Episode),
		AbsoluteNumber: int32(parsed.AbsoluteNumber),
		Quality:        parsed.Quality,
		Extension:      filepath.Ext(parsed.FileName),
		Edition:        edition,
		ReleaseGroup:   group,
		AirDate:        parsed.AirDate,
		Proper:         parseProperToken(parsed.FileName),
		TemplateId:     templateID,
	}
	if parsed.TMDBID > 0 {
		req.TmdbId = strconv.Itoa(parsed.TMDBID)
	}
	if parsed.MediaType == "tv" {
		if ep := m.lookupEpisodeRenameMeta(ctx, parsed); ep != nil && ep.GetFound() {
			if ep.GetEpisodeTitle() != "" {
				req.EpisodeTitle = ep.GetEpisodeTitle()
			}
			if ep.GetAirDate() != "" {
				req.AirDate = ep.GetAirDate()
			}
			if ep.GetSeriesName() != "" {
				req.Title = ep.GetSeriesName()
			}
			if ep.GetOriginalName() != "" {
				req.OriginalTitle = ep.GetOriginalName()
			}
		}
	}
	if meta := m.probeRenameMeta(fullPath); meta != nil {
		if meta.Resolution != "" {
			req.Resolution = meta.Resolution
		}
		if meta.Source != "" {
			req.Source = meta.Source
		}
		if meta.VideoCodec != "" {
			req.VideoCodec = meta.VideoCodec
		}
		if meta.AudioCodec != "" {
			req.AudioCodec = meta.AudioCodec
		}
		if meta.AudioChannels != "" {
			req.AudioChannels = meta.AudioChannels
		}
	}

	resp, err := cli.Preview(ctx, req)
	if err != nil {
		slog.Debug("rename preview failed", "error", err)
		return nil
	}
	return resp
}

type renameProbeMeta struct {
	Resolution    string
	Source        string
	VideoCodec    string
	AudioCodec    string
	AudioChannels string
}

func (m *Module) probeRenameMeta(path string) *renameProbeMeta {
	if path == "" {
		return nil
	}
	ctx := context.Background()
	addr, err := m.findCapabilityAddr(ctx, "media.analyzer")
	if err != nil {
		return nil
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil
	}
	defer conn.Close()
	cli := ffprobev1.NewAnalysisServiceClient(conn)
	resp, err := cli.Analyze(ctx, &ffprobev1.AnalyzeRequest{FilePath: path})
	if err != nil || resp.GetError() != "" {
		return nil
	}
	meta := &renameProbeMeta{}
	if q := resp.GetQuality(); q != nil {
		meta.Resolution = q.GetResolution()
		meta.Source = q.GetSource()
		meta.VideoCodec = q.GetCodecGroup()
	}
	if v := resp.GetVideo(); v != nil {
		if meta.Resolution == "" {
			meta.Resolution = v.GetResolutionLabel()
		}
		if meta.VideoCodec == "" {
			meta.VideoCodec = v.GetCodec()
		}
	}
	if audios := resp.GetAudio(); len(audios) > 0 {
		a := audios[0]
		meta.AudioCodec = a.GetCodec()
		if a.GetChannels() > 0 {
			meta.AudioChannels = strconv.Itoa(int(a.GetChannels()))
		} else if a.GetChannelLayout() != "" {
			meta.AudioChannels = a.GetChannelLayout()
		}
	}
	return meta
}

func (m *Module) lookupEpisodeRenameMeta(ctx context.Context, parsed parsedFile) *tvmgmtv1.LookupEpisodeResponse {
	addr, err := m.findCapabilityAddr(ctx, "media.library.tv")
	if err != nil {
		return nil
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil
	}
	defer conn.Close()
	cli := tvmgmtv1.NewTvManagementServiceClient(conn)
	resp, err := cli.LookupEpisode(ctx, &tvmgmtv1.LookupEpisodeRequest{
		TmdbId:         int32(parsed.TMDBID),
		Title:          parsed.Title,
		Year:           int32(parsed.Year),
		SeasonNumber:   int32(parsed.Season),
		EpisodeNumber:  int32(parsed.Episode),
		EpisodeNumbers: episodeNumbersInt32(parsed.Episodes, parsed.Episode),
		AbsoluteNumber: int32(parsed.AbsoluteNumber),
		AirDate:        parsed.AirDate,
	})
	if err != nil {
		slog.Debug("episode lookup for rename failed", "error", err)
		return nil
	}
	return resp
}

func (m *Module) probeQuality(path string) string {
	if path == "" {
		return ""
	}
	ctx := context.Background()
	addr, err := m.findCapabilityAddr(ctx, "media.analyzer")
	if err != nil {
		return ""
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return ""
	}
	defer conn.Close()
	cli := ffprobev1.NewAnalysisServiceClient(conn)
	resp, err := cli.Analyze(ctx, &ffprobev1.AnalyzeRequest{FilePath: path})
	if err != nil {
		slog.Debug("ffprobe analyze failed", "path", path, "error", err)
		return ""
	}
	if resp.GetError() != "" {
		slog.Debug("ffprobe analyze failed", "path", path, "resp_error", resp.GetError())
		return ""
	}
	if q := resp.GetQuality(); q != nil && q.GetLabel() != "" {
		return q.GetLabel()
	}
	if v := resp.GetVideo(); v != nil && v.GetResolutionLabel() != "" {
		return v.GetResolutionLabel()
	}
	return ""
}

var sidecarSubtitleExts = map[string]bool{
	".srt": true,
	".ass": true,
	".ssa": true,
	".sub": true,
	".vtt": true,
}

func (m *Module) importSidecarSubtitles(srcVideo, destVideo, storageKey string) {
	srcDir := filepath.Dir(srcVideo)
	base := strings.TrimSuffix(filepath.Base(srcVideo), filepath.Ext(srcVideo))
	destStem := strings.TrimSuffix(filepath.Base(destVideo), filepath.Ext(destVideo))
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	destDir := filepath.Dir(destVideo)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !sidecarSubtitleExts[ext] {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if !strings.HasPrefix(stem, base) {
			continue
		}
		suffix := strings.TrimPrefix(stem, base) // e.g. ".en" or ".en.forced"
		destName := destStem + suffix + ext
		srcSub := filepath.Join(srcDir, name)
		destSub := filepath.Join(destDir, destName)
		subKey := filepath.ToSlash(filepath.Join(filepath.Dir(storageKey), destName))

		registeredPath := ""
		if m.mc != nil {
			f, err := os.Open(srcSub)
			if err != nil {
				continue
			}
			if err := m.mc.Storage.Put(context.Background(), subKey, f); err != nil {
				f.Close()
				slog.Debug("storage put subtitle failed", "key", subKey, "error", err)
			} else {
				f.Close()
				os.Remove(srcSub)
			}
			continue
		}
		if err := os.MkdirAll(destDir, 0755); err != nil {
			continue
		}
		if err := copyFile(srcSub, destSub); err != nil {
			slog.Debug("copy subtitle failed", "src", srcSub, "error", err)
			continue
		}
		os.Remove(srcSub)
		registeredPath = destSub
		m.registerSidecarSubtitle(storageKey, registeredPath)
	}
}

func (m *Module) registerSidecarSubtitle(mediaFileID, filePath string) {
	if mediaFileID == "" || filePath == "" {
		return
	}
	if _, err := os.Stat(filePath); err != nil {
		return
	}
	ctx := context.Background()
	addr, err := m.findCapabilityAddr(ctx, "media.subtitles")
	if err != nil {
		return
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return
	}
	defer conn.Close()
	cli := subtv1.NewSubtitleServiceClient(conn)
	resp, err := cli.RegisterSidecar(ctx, &subtv1.RegisterSidecarRequest{
		MediaFileId: mediaFileID,
		FilePath:    filePath,
	})
	if err != nil {
		slog.Debug("register sidecar failed", "path", filePath, "error", err)
		return
	}
	if resp.GetAlreadyRegistered() {
		return
	}
	if sub := resp.GetSubtitle(); sub != nil {
		slog.Info("registered sidecar subtitle", "id", sub.GetId(), "lang", sub.GetLanguage(), "media", mediaFileID)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func (m *Module) detectEmbeddedSubtitles(path string) {
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	ctx := context.Background()
	addr, err := m.findCapabilityAddr(ctx, "media.subtitles")
	if err != nil {
		return
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return
	}
	defer conn.Close()
	cli := subtv1.NewSubtitleServiceClient(conn)
	resp, err := cli.DetectEmbedded(ctx, &subtv1.DetectEmbeddedRequest{FilePath: path})
	if err != nil {
		slog.Debug("detect embedded subtitles failed", "path", path, "error", err)
		return
	}
	if n := len(resp.GetSubtitles()); n > 0 {
		slog.Info("detected embedded subtitles", "path", path, "count", n)
	}
}

func (m *Module) buildStorageKey(p parsedFile) string {
	safeTitle := sanitizeName(p.Title)
	ext := filepath.Ext(p.FileName)
	switch p.MediaType {
	case "movie":
		yearStr := ""
		if p.Year > 0 {
			yearStr = fmt.Sprintf(" (%d)", p.Year)
		}
		base := fmt.Sprintf("%s.%d%s", safeTitle, p.Year, ext)
		if p.Quality != "" {
			base = fmt.Sprintf("%s.%d.%s%s", safeTitle, p.Year, p.Quality, ext)
		}
		return filepath.Join("media", "Movies", safeTitle+yearStr, base)
	case "tv":
		seasonDir := fmt.Sprintf("Season %02d", p.Season)
		tag := formatEpisodeTag(p.Season, p.Episodes, p.AbsoluteNumber)
		epBase := fmt.Sprintf("%s.%s", safeTitle, tag)
		if p.Quality != "" {
			epBase = fmt.Sprintf("%s.%s.%s", safeTitle, tag, p.Quality)
		}
		return filepath.Join("media", "TV", safeTitle, seasonDir, epBase+ext)
	default:
		return filepath.Join("media", "Other", safeTitle, p.FileName)
	}
}

func (m *Module) buildDestPath(p parsedFile, libPath, tvLibPath string) string {
	root := m.destRootFor(p, libPath, tvLibPath)
	folder := libraryKindPrefix(root, p.MediaType)
	safeTitle := sanitizeName(p.Title)
	if p.MediaType == "tv" {
		safeTitle = existingTitleDir(root, folder, p.Title)
	}
	ext := filepath.Ext(p.FileName)

	join := func(elem ...string) string {
		parts := make([]string, 0, 1+len(elem))
		parts = append(parts, root)
		if folder != "" {
			parts = append(parts, folder)
		}
		parts = append(parts, elem...)
		return filepath.Join(parts...)
	}

	switch p.MediaType {
	case "movie":
		yearStr := ""
		if p.Year > 0 {
			yearStr = fmt.Sprintf(" (%d)", p.Year)
		}
		base := fmt.Sprintf("%s.%d%s", safeTitle, p.Year, ext)
		if p.Quality != "" {
			base = fmt.Sprintf("%s.%d.%s%s", safeTitle, p.Year, p.Quality, ext)
		}
		return join(safeTitle+yearStr, base)

	case "tv":
		seasonDir := fmt.Sprintf("Season %02d", p.Season)
		tag := formatEpisodeTag(p.Season, p.Episodes, p.AbsoluteNumber)
		epBase := fmt.Sprintf("%s.%s", safeTitle, tag)
		if p.Quality != "" {
			epBase = fmt.Sprintf("%s.%s.%s", safeTitle, tag, p.Quality)
		}
		return join(safeTitle, seasonDir, epBase+ext)

	default:
		return join(safeTitle, p.FileName)
	}
}

// ── Filename Parsing ───────────────────────────────────────────

var (
	reMovie      = regexp.MustCompile(`(?i)^(.+?)[.\s-_]+(\d{4})(?:[.\s-_]+(.+))?\.\w+$`)
	reMovieParen = regexp.MustCompile(`(?i)^(.+?)\s*\((\d{4})\)`)
	reTMDBID     = regexp.MustCompile(`(?i)\[tmdbid-(\d+)\]`)
	reTitleYear  = regexp.MustCompile(`(?i)\s*[\(\[]((?:19|20)\d{2})[\)\]]\s*$`)
	reSeasonDir  = regexp.MustCompile(`(?i)^(?:season\s*\d{1,2}|specials)$`)

	reTVMultiEpRange  = regexp.MustCompile(`(?i)[.\s-_]+S(\d{1,2})E(\d{1,3})-E?(\d{1,3})(?:[.\s-_]|$|\.)`)
	reTVMultiEpChain  = regexp.MustCompile(`(?i)[.\s-_]+S(\d{1,2})E(\d{1,3})((?:E\d{1,3}){1,20})(?:[.\s-_]|$|\.)`)
	reTVNxNRange      = regexp.MustCompile(`(?i)[.\s-_]+(\d{1,2})x(\d{1,3})-(\d{1,2})x(\d{1,3})(?:[.\s-_]|$|\.)`)
	reTVSeasonEpisode = regexp.MustCompile(`(?i)[.\s-_]+S(\d{1,2})[.\s-_]*E(\d{1,3})(?:[.\s-_]|$|\.)`)
	reTVLeadingEp     = regexp.MustCompile(`(?i)^E(?:P)?(\d{1,3})(?:P\d+)?(?:[.\s-_]+)(.+)$`)
	reTVSeasonEpLong  = regexp.MustCompile(`(?i)[.\s-_]+(\d{1,2})x(\d{1,3})(?:[.\s-_]|$|\.)`)
	reTVSeasonOnly    = regexp.MustCompile(`(?i)[.\s-_]+Season[.\s-_]+(\d{1,2})(?:[.\s-_]|$|\.)`)
	reTVSeasonCode    = regexp.MustCompile(`(?i)[.\s-_]+S(\d{1,2})(?:[.\s-_]|$|\.)`)
	reDashEpisode     = regexp.MustCompile(`(?i)\s+-\s+(\d{2,3})(?:\s*[.\[]|$)`)
	reTVEpisodeOnly   = regexp.MustCompile(`(?i)[.\s-_]+Episode[.\s-_]+(\d{1,3})(?:[.\s-_]|$|\.)`)
	reTVAirDate       = regexp.MustCompile(`(?i)[.\s-_]+(20\d{2}|19\d{2})[.\s-_](\d{2})[.\s-_](\d{2})(?:[.\s-_]|$|\.)`)
	reAbsoluteBracket = regexp.MustCompile(`(?i)\[(\d{2,4})\]`)
	reAbsoluteEP      = regexp.MustCompile(`(?i)[.\s-_](?:EP|E)(\d{2,4})(?:[.\s-_]|$|\.)`)
	reAbsoluteDash    = regexp.MustCompile(`(?i)-\.?(\d{2,4})(?:[.\s-_]|$)`)
	reSeasonPack      = regexp.MustCompile(`(?i)(?:season[.\s_-]*pack|complete[.\s_-]*season|season[.\s_-]*\d{1,2}[.\s_-]*complete|S\d{1,2}[.\s_-]*(?:complete|pack))`)

	reQuality = regexp.MustCompile(`(?i)(\d{3,4}[pi]|4k|uhd)[.\s-_]*(remux|bluray|brrip|bdrip|blu-ray|web[-\s]?dl|webdl|webrip|hdtv)?`)

	reCleanTitle = regexp.MustCompile(`[.\s-_]+`)
)

type parsedFile struct {
	FileName       string
	Title          string
	Year           int
	Season         int
	Episode        int
	Episodes       []int
	AbsoluteNumber int
	AirDate        string // YYYY-MM-DD
	SeasonPack     bool
	Quality        string
	MediaType      string
	TMDBID         int
}

func parseFileName(fileName string) parsedFile {
	result := parsedFile{FileName: fileName}
	result.SeasonPack = reSeasonPack.MatchString(fileName)

	if m := reTVMultiEpRange.FindStringSubmatch(fileName); len(m) >= 4 {
		result.MediaType = "tv"
		result.Season, _ = strconv.Atoi(m[1])
		start, _ := strconv.Atoi(m[2])
		end, _ := strconv.Atoi(m[3])
		result.Episodes = episodeRange(start, end)
		result.Episode = start
		result.Title = extractTVTitle(fileName, m[0])
	} else if m := reTVMultiEpChain.FindStringSubmatch(fileName); len(m) >= 4 {
		result.MediaType = "tv"
		result.Season, _ = strconv.Atoi(m[1])
		first, _ := strconv.Atoi(m[2])
		result.Episodes = []int{first}
		for _, part := range reTVEpToken.FindAllStringSubmatch(m[3], -1) {
			if len(part) >= 2 {
				n, _ := strconv.Atoi(part[1])
				result.Episodes = append(result.Episodes, n)
			}
		}
		result.Episode = first
		result.Title = extractTVTitle(fileName, m[0])
	} else if m := reTVNxNRange.FindStringSubmatch(fileName); len(m) >= 5 {
		result.MediaType = "tv"
		result.Season, _ = strconv.Atoi(m[1])
		start, _ := strconv.Atoi(m[2])
		endSeason, _ := strconv.Atoi(m[3])
		end, _ := strconv.Atoi(m[4])
		if endSeason == result.Season {
			result.Episodes = episodeRange(start, end)
			result.Episode = start
		} else {
			result.Episode = start
			result.Episodes = []int{start}
		}
		result.Title = extractTVTitle(fileName, m[0])
	} else if m := reTVSeasonEpisode.FindStringSubmatch(fileName); len(m) >= 3 {
		result.MediaType = "tv"
		result.Season, _ = strconv.Atoi(m[1])
		result.Episode, _ = strconv.Atoi(m[2])
		result.Episodes = []int{result.Episode}
		result.Title = extractTVTitle(fileName, m[0])
	} else if m := reTVSeasonEpLong.FindStringSubmatch(fileName); len(m) >= 3 && !isVideoCodecNxN(m[2]) {
		result.MediaType = "tv"
		result.Season, _ = strconv.Atoi(m[1])
		result.Episode, _ = strconv.Atoi(m[2])
		result.Episodes = []int{result.Episode}
		result.Title = extractTVTitle(fileName, m[0])
	} else if m := reTVSeasonCode.FindStringSubmatch(fileName); len(m) >= 2 {
		result.MediaType = "tv"
		result.Season, _ = strconv.Atoi(m[1])
		result.Title = extractTVTitle(fileName, m[0])
	} else if reTVSeasonOnly.MatchString(fileName) || reTVEpisodeOnly.MatchString(fileName) {
		result.MediaType = "tv"
		if m := reTVSeasonOnly.FindStringSubmatch(fileName); len(m) >= 2 {
			result.Season, _ = strconv.Atoi(m[1])
		}
		if m := reTVEpisodeOnly.FindStringSubmatch(fileName); len(m) >= 2 {
			result.Episode, _ = strconv.Atoi(m[1])
			result.Episodes = []int{result.Episode}
		}
		result.Title = extractTVTitle(fileName, "")
	} else if airDate, title, ok := parseAirDate(fileName); ok {
		result.MediaType = "tv"
		result.AirDate = airDate
		result.Title = title
		if len(airDate) >= 4 {
			result.Year, _ = strconv.Atoi(airDate[:4])
		}
	} else if abs, ok := parseAbsoluteNumber(fileName); ok {
		result.MediaType = "tv"
		result.AbsoluteNumber = abs
		result.Title = extractTVTitleAbsolute(fileName)
	} else if m := reTVLeadingEp.FindStringSubmatch(trimExt(fileName)); len(m) >= 3 {
		result.MediaType = "tv"
		n, _ := strconv.Atoi(m[1])
		result.AbsoluteNumber = n
		result.Episode = n
		result.Episodes = []int{n}
		result.Title = cleanTitle(m[2])
	} else if n, ok := parseBareEpisodeNumber(fileName); ok {
		result.MediaType = "tv"
		result.Episode = n
		result.Episodes = []int{n}
		result.Title = ""
	} else if m := reMovieParen.FindStringSubmatch(fileName); len(m) >= 3 {
		result.MediaType = "movie"
		result.Title = cleanTitle(m[1])
		result.Year, _ = strconv.Atoi(m[2])
	} else if m := reMovie.FindStringSubmatch(fileName); len(m) >= 3 {
		result.MediaType = "movie"
		result.Title = cleanTitle(m[1])
		result.Year, _ = strconv.Atoi(m[2])
		if len(m) >= 4 && m[3] != "" {
			result.Quality = m[3]
		}
	} else {
		result.MediaType = "other"
		result.Title = cleanTitle(trimExt(fileName))
	}

	if tm := reTMDBID.FindStringSubmatch(fileName); len(tm) >= 2 {
		result.TMDBID, _ = strconv.Atoi(tm[1])
	}

	if result.MediaType == "tv" {
		result.Title, result.Year = stripTrailingYear(result.Title, result.Year)
		if result.Episode == 0 && len(result.Episodes) == 0 {
			if m := reDashEpisode.FindStringSubmatch(fileName); len(m) >= 2 {
				n, _ := strconv.Atoi(m[1])
				if n > 0 {
					result.Episode = n
					result.Episodes = []int{n}
				}
			}
		}
	}

	result.Quality = extractQuality(fileName)
	return result
}

func enrichParsedFromPath(parsed *parsedFile, fullPath string) {
	if parsed == nil {
		return
	}
	if tm := reTMDBID.FindStringSubmatch(fullPath); len(tm) >= 2 && parsed.TMDBID == 0 {
		parsed.TMDBID, _ = strconv.Atoi(tm[1])
	}
	parent := filepath.Base(filepath.Dir(fullPath))
	if isSeasonDirName(parent) {
		parent = filepath.Base(filepath.Dir(filepath.Dir(fullPath)))
	}
	if parent == "" || parent == "." || skipLibrarySubdir(parent) {
		if parsed.Title != "" && parsed.Year > 0 && parsed.MediaType != "other" {
			return
		}
		return
	}
	fromDir := parseFileName(parent + ".mkv")
	if n, ok := parseBareEpisodeNumber(parsed.FileName); ok && fromDir.Title != "" && !isJunkImportTitle(fromDir.Title) {
		parsed.Title = fromDir.Title
		parsed.MediaType = "tv"
		parsed.Episode = n
		parsed.Episodes = []int{n}
		if parsed.Season == 0 && fromDir.Season > 0 {
			parsed.Season = fromDir.Season
		}
		if parsed.Year == 0 {
			parsed.Year = fromDir.Year
		}
		return
	}
	if looksLikeBareEpisodeFile(parsed.FileName) && !looksLikeBareEpisodeFile(parent+".mkv") && fromDir.Title != "" {
		parsed.Title = fromDir.Title
		parsed.MediaType = "tv"
		if parsed.Year == 0 {
			parsed.Year = fromDir.Year
		}
	}
	if parsed.Title != "" && parsed.Year > 0 && parsed.MediaType != "other" {
		return
	}
	if parsed.Title == "" {
		parsed.Title = fromDir.Title
	}
	if parsed.Year == 0 {
		parsed.Year = fromDir.Year
	}
	if parsed.TMDBID == 0 {
		parsed.TMDBID = fromDir.TMDBID
	}
	if parsed.MediaType == "other" && fromDir.MediaType != "" {
		parsed.MediaType = fromDir.MediaType
	}
}

func looksLikeBareEpisodeFile(fileName string) bool {
	return reTVLeadingEp.MatchString(trimExt(fileName))
}

func looksLikeTVName(name string) bool {
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		base := filepath.Base(part)
		if reTVSeasonEpisode.MatchString(base) || reTVSeasonCode.MatchString(base) || reTVSeasonOnly.MatchString(base) {
			return true
		}
		if looksLikeBareEpisodeFile(base) {
			return true
		}
	}
	return false
}

var reTVEpToken = regexp.MustCompile(`(?i)E(\d{1,3})`)

func episodeRange(start, end int) []int {
	if end < start {
		start, end = end, start
	}
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

// isVideoCodecNxN reports whether an NxN capture is a scene codec (DDP5.1.x265), not 1x265.
func isVideoCodecNxN(episode string) bool {
	switch episode {
	case "264", "265", "266":
		return true
	default:
		return false
	}
}

func parseAbsoluteNumber(fileName string) (int, bool) {
	if m := reAbsoluteBracket.FindStringSubmatch(fileName); len(m) >= 2 {
		n, _ := strconv.Atoi(m[1])
		if n > 0 {
			return n, true
		}
	}
	if m := reAbsoluteEP.FindStringSubmatch(fileName); len(m) >= 2 {
		n, _ := strconv.Atoi(m[1])
		if n > 0 {
			return n, true
		}
	}
	if m := reAbsoluteDash.FindStringSubmatch(fileName); len(m) >= 2 {
		n, _ := strconv.Atoi(m[1])
		if n > 0 {
			return n, true
		}
	}
	return 0, false
}

func parseAirDate(fileName string) (airDate, title string, ok bool) {
	m := reTVAirDate.FindStringSubmatch(fileName)
	if len(m) < 4 {
		return "", "", false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	if y < 1900 || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return "", "", false
	}
	airDate = fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
	title = extractTVTitle(fileName, m[0])
	return airDate, title, true
}

func formatEpisodeTag(season int, episodes []int, absolute int) string {
	if absolute > 0 && len(episodes) == 0 && season == 0 {
		return fmt.Sprintf("%03d", absolute)
	}
	if len(episodes) == 0 {
		return fmt.Sprintf("S%02dE%02d", season, 0)
	}
	if len(episodes) == 1 {
		return fmt.Sprintf("S%02dE%02d", season, episodes[0])
	}
	return fmt.Sprintf("S%02dE%02d-E%02d", season, episodes[0], episodes[len(episodes)-1])
}

func episodeNumbersInt32(episodes []int, fallback int) []int32 {
	if len(episodes) == 0 {
		if fallback > 0 {
			return []int32{int32(fallback)}
		}
		return nil
	}
	out := make([]int32, len(episodes))
	for i, e := range episodes {
		out[i] = int32(e)
	}
	return out
}

func stripTrailingYear(title string, year int) (string, int) {
	title = strings.TrimSpace(title)
	m := reTitleYear.FindStringSubmatch(title)
	if len(m) < 2 {
		return title, year
	}
	y, _ := strconv.Atoi(m[1])
	if y < 1900 {
		return title, year
	}
	title = strings.TrimSpace(reTitleYear.ReplaceAllString(title, ""))
	if year == 0 {
		year = y
	}
	return title, year
}

func isSeasonDirName(name string) bool {
	return reSeasonDir.MatchString(strings.TrimSpace(name))
}

func extractTVTitle(fileName, match string) string {
	s := fileName
	ext := filepath.Ext(s)
	s = s[:len(s)-len(ext)]

	if match != "" {
		idx := strings.Index(strings.ToLower(s), strings.ToLower(strings.Trim(match, ".-_ ")))
		if idx < 0 {
			parts := strings.SplitN(s, strings.TrimSpace(match), 2)
			if len(parts) > 0 {
				return cleanTitle(parts[0])
			}
		} else if idx > 0 {
			return cleanTitle(s[:idx])
		}
	}

	s = reTVMultiEpRange.ReplaceAllString(s, " ")
	s = reTVMultiEpChain.ReplaceAllString(s, " ")
	s = reTVNxNRange.ReplaceAllString(s, " ")
	s = reTVSeasonEpisode.ReplaceAllString(s, " ")
	s = reTVSeasonEpLong.ReplaceAllString(s, " ")
	s = reTVSeasonOnly.ReplaceAllString(s, " ")
	s = reTVEpisodeOnly.ReplaceAllString(s, " ")
	s = reTVAirDate.ReplaceAllString(s, " ")
	return cleanTitle(s)
}

func extractTVTitleAbsolute(fileName string) string {
	s := trimExt(fileName)
	s = reAbsoluteBracket.ReplaceAllString(s, " ")
	s = reAbsoluteEP.ReplaceAllString(s, " ")
	s = reAbsoluteDash.ReplaceAllString(s, " ")
	s = reSeasonPack.ReplaceAllString(s, " ")
	return cleanTitle(s)
}

func extractQuality(name string) string {
	m := reQuality.FindString(name)
	return strings.Trim(m, ".-_ \t")
}

func cleanTitle(s string) string {
	s = reCleanTitle.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	var filtered []string
	skipWords := map[string]bool{
		"proper": true, "repack": true, "internal": true,
		"readnfo": true, "remux": true, "bluray": true, "webdl": true,
	}
	for _, p := range parts {
		if !skipWords[strings.ToLower(p)] {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, " ")
}

func trimExt(s string) string {
	ext := filepath.Ext(s)
	return s[:len(s)-len(ext)]
}

func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "?", "_")
	s = strings.ReplaceAll(s, "\"", "_")
	s = strings.ReplaceAll(s, "<", "_")
	s = strings.ReplaceAll(s, ">", "_")
	s = strings.ReplaceAll(s, "|", "_")
	s = strings.ReplaceAll(s, "*", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return strings.TrimSpace(s)
}

func parseBareEpisodeNumber(fileName string) (int, bool) {
	base := trimExt(filepath.Base(fileName))
	if base == "" {
		return 0, false
	}
	for _, r := range base {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(base)
	if err != nil || n < 1 || n > 399 {
		return 0, false
	}
	switch n {
	case 480, 576, 720, 1080, 2160:
		return 0, false
	}
	return n, true
}

func isJunkImportTitle(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	switch t {
	case "rarbg", "rarbg.com", "www", "proof", "proofs", "sample", "samples",
		"screens", "screen", "nfo", "nfos", "new", "complete", "pack":
		return true
	}
	if strings.HasPrefix(t, "www.") {
		return true
	}
	if _, ok := parseBareEpisodeNumber(t + ".mkv"); ok {
		return true
	}
	return false
}

func existingTitleDir(root, kindPrefix, title string) string {
	safe := sanitizeName(title)
	if safe == "" {
		return safe
	}
	parent := root
	if kindPrefix != "" {
		parent = filepath.Join(root, kindPrefix)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return safe
	}
	want := strings.ToLower(safe)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.ToLower(e.Name()) == want {
			return e.Name()
		}
	}
	return safe
}

// ── gRPC API ───────────────────────────────────────────────────

func (m *Module) Scan(ctx context.Context, req *scannerv1.ScanRequest) (*scannerv1.ScanResponse, error) {
	dirs := m.collectWatchDirs()
	if dirs == nil {
		return nil, fmt.Errorf("not initialized")
	}

	var totalFound, totalImported, totalSkipped int
	for _, d := range dirs {
		found, imported, skipped := m.scanDirectory(d.path, d.mediaType, d.libPath, d.tvLibPath)
		totalFound += found
		totalImported += imported
		totalSkipped += skipped
	}

	return &scannerv1.ScanResponse{
		FilesFound:    int32(totalFound),
		FilesImported: int32(totalImported),
		FilesSkipped:  int32(totalSkipped),
	}, nil
}

func (m *Module) ImportPath(ctx context.Context, req *scannerv1.ImportPathRequest) (*scannerv1.ImportPathResponse, error) {
	path := strings.TrimSpace(req.GetPath())
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	path = filepath.Clean(path)

	dirs := withPartialsImportRoots(m.collectWatchDirs())
	if dirs == nil {
		return nil, fmt.Errorf("not initialized")
	}

	if !filepath.IsAbs(path) {
		orig := path
		if resolved, ok := resolveRelativeWatchPath(dirs, path); ok {
			slog.Info("ImportPath resolved relative path", "from", orig, "to", resolved)
			path = resolved
		} else {
			slog.Info("ImportPath relative path unresolved", "path", orig)
		}
	}

	d, ok := deepestWatchDir(dirs, path)
	if !ok {
		return nil, fmt.Errorf("path %q is not under any registered watch directory", path)
	}

	m.scanMu.Lock()
	defer m.scanMu.Unlock()

	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		found, imported, skipped := 1, 0, 0
		if m.isAlreadyImported(path) {
			skipped = 1
		} else if m.importFile(path, filepath.Base(path), d.mediaType, d.libPath, d.tvLibPath) {
			imported = 1
		} else {
			skipped = 1
		}
		return &scannerv1.ImportPathResponse{
			FilesFound:    int32(found),
			FilesImported: int32(imported),
			FilesSkipped:  int32(skipped),
		}, nil
	}

	found, imported, skipped := m.scanDirectory(path, d.mediaType, d.libPath, d.tvLibPath)
	return &scannerv1.ImportPathResponse{
		FilesFound:    int32(found),
		FilesImported: int32(imported),
		FilesSkipped:  int32(skipped),
	}, nil
}

func deepestWatchDir(dirs []watchDirEntry, path string) (watchDirEntry, bool) {
	clean := filepath.Clean(path)
	var best watchDirEntry
	bestLen := -1
	for _, d := range dirs {
		root := filepath.Clean(d.path)
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			if len(root) > bestLen {
				best = d
				bestLen = len(root)
			}
		}
	}
	return best, bestLen >= 0
}

// withPartialsImportRoots lets ImportPath accept completed torrents that landed
// in {cwd}/partials or {watchDir}/partials when keep_stalled_partials used a
// relative save path. These roots are not added to the watch/scan loop.
func withPartialsImportRoots(dirs []watchDirEntry) []watchDirEntry {
	if dirs == nil {
		return nil
	}
	lib, tvLib, mt := "", "", "both"
	seen := map[string]struct{}{}
	for _, d := range dirs {
		seen[filepath.Clean(d.path)] = struct{}{}
		if strings.TrimSpace(d.libPath) != "" {
			lib = d.libPath
		}
		if strings.TrimSpace(d.tvLibPath) != "" {
			tvLib = d.tvLibPath
		}
		if d.mediaType != "" {
			mt = d.mediaType
		}
	}
	add := func(p string) {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || p == "." {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			return
		}
		seen[p] = struct{}{}
		dirs = append(dirs, watchDirEntry{path: p, mediaType: mt, libPath: lib, tvLibPath: tvLib})
	}
	if cwd, err := os.Getwd(); err == nil {
		add(filepath.Join(cwd, "partials"))
	}
	orig := append([]watchDirEntry(nil), dirs...)
	for _, d := range orig {
		add(filepath.Join(d.path, "partials"))
	}
	return dirs
}

// resolveRelativeWatchPath joins a relative import path with each watch dir and
// returns the candidate that exists on disk (deepest watch root wins).
func resolveRelativeWatchPath(dirs []watchDirEntry, rel string) (string, bool) {
	rel = strings.TrimPrefix(filepath.Clean(rel), string(os.PathSeparator))
	if rel == "." || rel == "" {
		return "", false
	}
	var best string
	bestLen := -1
	try := func(root, cand string) {
		if _, err := os.Stat(cand); err != nil {
			return
		}
		if len(root) > bestLen {
			best = cand
			bestLen = len(root)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		try(cwd, filepath.Join(cwd, rel))
	}
	for _, d := range dirs {
		root := filepath.Clean(d.path)
		try(root, filepath.Join(root, rel))
	}
	return best, bestLen >= 0
}

func (m *Module) ScanLibraryRoots(ctx context.Context, req *scannerv1.ScanLibraryRootsRequest) (*scannerv1.ScanLibraryRootsResponse, error) {
	targets := m.libraryScanTargets()
	if targets == nil {
		return nil, fmt.Errorf("not initialized")
	}
	var totalFound, totalImported, totalSkipped int
	for _, t := range targets {
		found, imported, skipped := m.scanLibraryDirectory(t.dir, t.mediaType)
		totalFound += found
		totalImported += imported
		totalSkipped += skipped
	}
	return &scannerv1.ScanLibraryRootsResponse{
		FilesFound:    int32(totalFound),
		FilesImported: int32(totalImported),
		FilesSkipped:  int32(totalSkipped),
	}, nil
}

type libraryScanTarget struct {
	dir       string
	mediaType string
}

func (m *Module) libraryScanTargets() []libraryScanTarget {
	m.mu.RLock()
	dbReady := m.db != nil
	tvRoot := m.tvLibraryRoot
	m.mu.RUnlock()
	if !dbReady {
		return nil
	}
	seen := map[string]struct{}{}
	var out []libraryScanTarget
	add := func(dir, mediaType string) {
		dir = filepath.Clean(dir)
		if dir == "" || dir == "." {
			return
		}
		key := mediaType + "\x00" + dir
		if _, ok := seen[key]; ok {
			return
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return
		}
		seen[key] = struct{}{}
		out = append(out, libraryScanTarget{dir: dir, mediaType: mediaType})
	}
	for _, root := range m.collectLibraryRoots() {
		for _, dir := range resolveLibraryDirs(root, "movie") {
			add(dir, "movie")
		}
		for _, dir := range resolveLibraryDirs(root, "tv") {
			add(dir, "tv")
		}
	}
	if tvRoot != "" {
		for _, dir := range resolveLibraryDirs(tvRoot, "tv") {
			add(dir, "tv")
		}
	}
	if out == nil {
		out = []libraryScanTarget{}
	}
	return out
}

func resolveLibraryDirs(root, mediaType string) []string {
	root = filepath.Clean(root)
	if root == "" || root == "." {
		return nil
	}
	names := movieLibraryDirNames
	if mediaType == "tv" {
		names = tvLibraryDirNames
	}
	base := strings.ToLower(filepath.Base(root))
	for _, name := range names {
		if base == strings.ToLower(name) {
			return []string{root}
		}
	}
	var found []string
	for _, name := range names {
		p := filepath.Join(root, name)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			found = append(found, p)
		}
	}
	if len(found) > 0 {
		return found
	}
	other := tvLibraryDirNames
	if mediaType == "tv" {
		other = movieLibraryDirNames
	}
	for _, name := range other {
		if base == strings.ToLower(name) {
			return nil
		}
	}
	if hasMediaFiles(root, 2) {
		return []string{root}
	}
	return nil
}

var (
	movieLibraryDirNames = []string{"Movies", "movies", "movie"}
	tvLibraryDirNames    = []string{"TV", "tv", "shows", "Shows"}
)

func hasMediaFiles(dir string, depth int) bool {
	if depth < 0 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if skipLibrarySubdir(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if hasMediaFiles(p, depth-1) {
				return true
			}
			continue
		}
		if isMediaExt(strings.ToLower(filepath.Ext(e.Name()))) {
			return true
		}
	}
	return false
}

func skipBonusDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "extras", "extra", "samples", "sample", "featurettes", "featurette", "trailers", "trailer", "bonus",
		"rarbg", "screens", "screen", "proofs", "proof", "nfo", "nfos":
		return true
	default:
		return false
	}
}

func pathInBonusDir(fullPath string) bool {
	for _, p := range strings.Split(filepath.Clean(fullPath), string(os.PathSeparator)) {
		if skipBonusDir(p) {
			return true
		}
	}
	return false
}

func skipLibrarySubdir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "extras", "extra", "samples", "sample", "featurettes", "trailers", "other", "tv", "rarbg":
		return true
	default:
		return false
	}
}

func (m *Module) collectLibraryRoots() []string {
	registered, hasRegistered := m.listRegisteredRoots(context.Background())

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var roots []string
	add := func(p string) {
		p = filepath.Clean(p)
		if p == "" || p == "." {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		roots = append(roots, p)
	}
	if hasRegistered {
		for _, r := range registered {
			add(r.GetPath())
		}
	}
	if m.libraryRoot != "" {
		add(m.libraryRoot)
	}
	rows, err := m.db.Query(`SELECT DISTINCT library_path FROM watch_dirs WHERE library_path != ''`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p string
			if rows.Scan(&p) == nil {
				add(p)
			}
		}
	}
	return roots
}

func (m *Module) scanLibraryDirectory(dir, mediaType string) (found, imported, skipped int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, 0
	}
	for _, entry := range entries {
		if skipLibrarySubdir(entry.Name()) {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			f, i, s := m.scanLibraryDirectory(fullPath, mediaType)
			found += f
			imported += i
			skipped += s
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !isMediaExt(ext) {
			continue
		}
		found++
		if m.isAlreadyImported(fullPath) {
			skipped++
			continue
		}
		if m.registerLibraryFile(fullPath, entry.Name(), mediaType) {
			imported++
		} else {
			skipped++
		}
	}
	return
}

func (m *Module) registerLibraryFile(fullPath, fileName, mediaType string) bool {
	parsed := parseFileName(fileName)
	enrichParsedFromPath(&parsed, fullPath)
	if parsed.Title == "" || isJunkImportTitle(parsed.Title) {
		return false
	}
	switch {
	case parsed.MediaType == mediaType:
		// ok
	case parsed.MediaType == "other" && (mediaType == "movie" || mediaType == "tv"):
		parsed.MediaType = mediaType
	default:
		return false
	}

	quality := parsed.Quality
	if q := m.probeQuality(fullPath); q != "" {
		quality = q
	}

	now := time.Now().UTC().Format(time.RFC3339)
	importID := fmt.Sprintf("imp_%d", time.Now().UnixNano())
	m.mu.Lock()
	m.db.Exec(`INSERT INTO imported_files (id, original_path, destination_path, file_name, media_type, title, year, season_number, episode_number, quality, tmdb_id, imported_at, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'imported')`,
		importID, fullPath, fullPath, fileName, parsed.MediaType, parsed.Title, parsed.Year, parsed.Season, parsed.Episode, quality, parsed.TMDBID, now,
	)
	m.mu.Unlock()

	go m.publish(context.Background(), contracts.EventFileImported, map[string]interface{}{
		"original_path":    fullPath,
		"destination_path": fullPath,
		"storage_key":      fullPath,
		"media_type":       parsed.MediaType,
		"title":            parsed.Title,
		"year":             parsed.Year,
		"season_number":    parsed.Season,
		"episode_number":   parsed.Episode,
		"episode_numbers":  episodeNumbersInt32(parsed.Episodes, parsed.Episode),
		"absolute_number":  parsed.AbsoluteNumber,
		"air_date":         parsed.AirDate,
		"season_pack":      parsed.SeasonPack,
		"quality":          quality,
		"tmdb_id":          parsed.TMDBID,
	})
	return true
}

func (m *Module) GetStats(ctx context.Context, req *scannerv1.GetStatsRequest) (*scannerv1.GetStatsResponse, error) {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	var totalImported, watchDirs int
	db.QueryRow(`SELECT COUNT(*) FROM imported_files WHERE status = 'imported'`).Scan(&totalImported)
	db.QueryRow(`SELECT COUNT(*) FROM watch_dirs WHERE enabled = 1`).Scan(&watchDirs)

	var lastScanAt int64
	var lastStatus string
	var startedAt string
	if err := db.QueryRow(`SELECT status, started_at FROM scan_log ORDER BY started_at DESC LIMIT 1`).Scan(&lastStatus, &startedAt); err == nil {
		if t, parseErr := time.Parse(time.RFC3339, startedAt); parseErr == nil {
			lastScanAt = t.Unix()
		}
	}

	return &scannerv1.GetStatsResponse{
		TotalImported:  int32(totalImported),
		WatchDirs:      int32(watchDirs),
		LastScanStatus: lastStatus,
		LastScanAt:     lastScanAt,
	}, nil
}

func (m *Module) AddWatchDir(ctx context.Context, req *scannerv1.AddWatchDirRequest) (*scannerv1.AddWatchDirResponse, error) {
	if req.GetPath() == "" {
		return nil, fmt.Errorf("path is required")
	}
	m.mu.Lock()
	if m.db == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("not initialized")
	}

	mediaType := req.GetMediaType()
	if mediaType == "" {
		mediaType = "both"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := fmt.Sprintf("wd_%d", time.Now().UnixNano())

	tvPath := m.tvLibraryRoot
	_, err := m.db.Exec(`INSERT OR IGNORE INTO watch_dirs (id, path, media_type, library_path, tv_library_path, enabled, created_at) VALUES (?, ?, ?, ?, ?, 1, ?)`,
		id, req.GetPath(), mediaType, req.GetLibraryPath(), tvPath, now)
	m.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("insert watch dir: %w", err)
	}

	slog.Info("added watch directory", "path", req.GetPath(), "type", mediaType)
	_ = m.syncWatches()
	return &scannerv1.AddWatchDirResponse{Id: id}, nil
}

func (m *Module) RemoveWatchDir(ctx context.Context, req *scannerv1.RemoveWatchDirRequest) (*scannerv1.RemoveWatchDirResponse, error) {
	m.mu.Lock()
	if m.db == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("not initialized")
	}
	_, err := m.db.Exec(`DELETE FROM watch_dirs WHERE id = ?`, req.GetId())
	m.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("delete watch dir: %w", err)
	}
	_ = m.syncWatches()
	return &scannerv1.RemoveWatchDirResponse{}, nil
}

func (m *Module) ListWatchDirs(ctx context.Context, req *scannerv1.ListWatchDirsRequest) (*scannerv1.ListWatchDirsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	rows, err := m.db.Query(`SELECT id, path, media_type, library_path, enabled, created_at FROM watch_dirs`)
	if err != nil {
		return nil, fmt.Errorf("query watch dirs: %w", err)
	}
	defer rows.Close()

	var dirs []*scannerv1.WatchDir
	for rows.Next() {
		var id, path, mediaType, libPath, createdAt string
		var enabled int
		if err := rows.Scan(&id, &path, &mediaType, &libPath, &enabled, &createdAt); err != nil {
			continue
		}
		dirs = append(dirs, &scannerv1.WatchDir{
			Id: id, Path: path, MediaType: mediaType,
			LibraryPath: libPath, Enabled: enabled != 0, CreatedAt: createdAt,
		})
	}
	return &scannerv1.ListWatchDirsResponse{Dirs: dirs}, nil
}

func (m *Module) ListImported(ctx context.Context, req *scannerv1.ListImportedRequest) (*scannerv1.ListImportedResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := `SELECT id, original_path, destination_path, file_name, media_type, title, year, season_number, episode_number, quality, tmdb_id, imported_at, status FROM imported_files`
	countQuery := `SELECT COUNT(*) FROM imported_files`
	var args []any
	var where []string

	if req.GetMediaType() != "" {
		where = append(where, `media_type = ?`)
		args = append(args, req.GetMediaType())
	}
	if len(where) > 0 {
		clause := ` WHERE ` + strings.Join(where, ` AND `)
		query += clause
		countQuery += clause
	}

	var total int
	m.db.QueryRowContext(ctx, countQuery, args...).Scan(&total)
	query += ` ORDER BY imported_at DESC LIMIT ? OFFSET ?`
	qargs := append(args, pageSize, offset)

	rows, err := m.db.QueryContext(ctx, query, qargs...)
	if err != nil {
		return nil, fmt.Errorf("query imported: %w", err)
	}
	defer rows.Close()

	var files []*scannerv1.ImportedFile
	for rows.Next() {
		var id, origPath, destPath, fname, mediaType, title, quality, importedAt, status string
		var year, seasonNum, epNum, tmdbID int64
		if err := rows.Scan(&id, &origPath, &destPath, &fname, &mediaType, &title, &year, &seasonNum, &epNum, &quality, &tmdbID, &importedAt, &status); err != nil {
			slog.Error("scan imported row", "error", err)
			continue
		}
		files = append(files, &scannerv1.ImportedFile{
			Id: id, OriginalPath: origPath, DestinationPath: destPath,
			FileName: fname, MediaType: mediaType, Title: title,
			Year: int32(year), SeasonNumber: int32(seasonNum),
			EpisodeNumber: int32(epNum), Quality: quality,
			TmdbId: int32(tmdbID), ImportedAt: importedAt, Status: status,
		})
	}

	return &scannerv1.ListImportedResponse{
		Files: files, Total: int32(total),
		Page: int32(page), PageSize: int32(pageSize),
	}, nil
}

func normalizeImportMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "hardlink", "link":
		return "hardlink"
	case "copy":
		return "copy"
	case "move":
		return "move"
	default:
		return "hardlink"
	}
}

func placeFile(src, dst, mode string) error {
	mode = normalizeImportMode(mode)
	switch mode {
	case "hardlink":
		if err := os.Link(src, dst); err == nil {
			return nil
		}
		return copyFile(src, dst)
	case "copy":
		return copyFile(src, dst)
	default:
		if err := os.Rename(src, dst); err == nil {
			return nil
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
		return os.Remove(src)
	}
}

var (
	reSampleName = regexp.MustCompile(`(?i)(?:^|[.\-_ ])(sample|samples|trailer)(?:[.\-_ ]|$)`)
	reSampleBase = regexp.MustCompile(`(?i)^(?:sample|samples|trailer)(?:[.\-_ ].*)?\.\w+$`)
	reExtraName  = regexp.MustCompile(`(?i)(?:^|[.\-_ ])(extras?|bonus|featurettes?|deleted[.\-_ ]?scenes?|alternate[.\-_ ]?scenes?|extended[.\-_ ]+or[.\-_ ]+alternate)(?:[.\-_ ]|$)`)
	reEdition    = regexp.MustCompile(`(?i)(?:^|[.\-_ ])(directors?[.\-_ ]?cut|extended(?:[.\-_ ]?cut)?|theatrical|unrated|remastered|criterion|imax)(?:[.\-_ ]|$)`)
	reGroup      = regexp.MustCompile(`(?i)-([A-Za-z0-9]+)(?:\.\w+)?$`)
	reProper     = regexp.MustCompile(`(?i)(?:^|[.\-_ ])(proper|repack)(?:[.\-_ ]|$)`)
)

func (m *Module) isSampleFile(name string, size int64) (reason string, reject bool) {
	if size == 0 {
		return "empty file", true
	}
	if strings.HasSuffix(strings.ToLower(name), ".part") {
		return "incomplete download", true
	}
	if m.minVideoBytes > 0 && size > 0 && size < m.minVideoBytes {
		return "below minimum video size", true
	}
	if reExtraName.MatchString(name) {
		return "extra/bonus filename", true
	}
	sampleName := reSampleName.MatchString(name) || reSampleBase.MatchString(name)
	if sampleName && (size == 0 || size < m.sampleMaxBytes) {
		return "sample/trailer filename", true
	}
	return "", false
}

func parseEditionAndGroup(name string) (edition, group string) {
	if m := reEdition.FindStringSubmatch(name); len(m) >= 2 {
		edition = strings.TrimSpace(strings.ReplaceAll(m[1], ".", " "))
		edition = strings.Join(strings.Fields(edition), " ")
	}
	if m := reGroup.FindStringSubmatch(name); len(m) >= 2 {
		group = m[1]
	}
	return edition, group
}

func parseProperToken(name string) string {
	m := reProper.FindStringSubmatch(name)
	if len(m) < 2 {
		return ""
	}
	switch strings.ToLower(m[1]) {
	case "proper":
		return "Proper"
	case "repack":
		return "Repack"
	default:
		return ""
	}
}

var _ contracts.Module = (*Module)(nil)
