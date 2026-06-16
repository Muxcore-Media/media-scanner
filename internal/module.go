package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

	scannerv1 "github.com/Muxcore-Media/media-scanner/proto/scannerv1"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/core/sdk/go/client"
	_ "modernc.org/sqlite"
)

type Module struct {
	scannerv1.UnimplementedScannerServiceServer

	mu sync.RWMutex
	db *sql.DB
	mc *client.Client

	id          string
	dbPath      string
	grpcAddr    string
	libraryRoot string
	grpcSrv     *grpc.Server
	grpcLis     net.Listener
}

type Config struct {
	ID          string
	DBPath      string
	GRPCAddr    string
	LibraryRoot string
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
	if v := os.Getenv("SCANNER_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("SCANNER_GRPC_ADDR"); v != "" {
		cfg.GRPCAddr = v
	}
	if v := os.Getenv("SCANNER_LIBRARY_ROOT"); v != "" {
		cfg.LibraryRoot = v
	}
	return &Module{
		id:          cfg.ID,
		dbPath:      cfg.DBPath,
		grpcAddr:    cfg.GRPCAddr,
		libraryRoot: cfg.LibraryRoot,
	}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:             m.id,
		Name:           "Media Scanner",
		Version:        "0.1.0",
		Roles:          []string{"scanner"},
		Description:    "Scans download directories, identifies media files, and imports them into the library",
		Author:         "MuxCore",
		Capabilities:   []string{"media.scanner"},
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
			id           TEXT PRIMARY KEY,
			path         TEXT NOT NULL UNIQUE,
			media_type   TEXT NOT NULL DEFAULT 'both',
			library_path TEXT NOT NULL DEFAULT '',
			enabled      INTEGER DEFAULT 1,
			created_at   TEXT NOT NULL
		)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create watch_dirs table: %w", err)
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

	go func() {
		slog.Info("media-scanner gRPC service started", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(m.grpcLis); err != nil {
			slog.Error("media-scanner gRPC serve error", "error", err)
		}
	}()

	go m.dialCore(context.Background())
	go m.scanLoop()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
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
	insecureMode := os.Getenv("MUXCORE_GRPC_INSECURE") == "true"
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

func (m *Module) scanLoop() {
	const interval = 60 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	time.Sleep(5 * time.Second)
	m.runScan()

	for range ticker.C {
		m.runScan()
	}
}

func (m *Module) runScan() {
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
		found, imported, skipped := m.scanDirectory(d.path, d.mediaType, d.libPath)
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
}

func (m *Module) collectWatchDirs() []watchDirEntry {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return nil
	}

	rows, err := db.Query(`SELECT path, media_type, library_path FROM watch_dirs WHERE enabled = 1`)
	if err != nil {
		slog.Error("query watch dirs", "error", err)
		return nil
	}
	defer rows.Close()

	var dirs []watchDirEntry
	for rows.Next() {
		var d watchDirEntry
		if err := rows.Scan(&d.path, &d.mediaType, &d.libPath); err != nil {
			slog.Error("scan watch dir row", "error", err)
			continue
		}
		dirs = append(dirs, d)
	}
	return dirs
}

func (m *Module) scanDirectory(watchPath, mediaType, libPath string) (found, imported, skipped int) {
	entries, err := os.ReadDir(watchPath)
	if err != nil {
		slog.Warn("cannot read directory", "path", watchPath, "error", err)
		return 0, 0, 0
	}

	for _, entry := range entries {
		if entry.IsDir() {
			subFound, subImported, subSkipped := m.scanDirectory(filepath.Join(watchPath, entry.Name()), mediaType, libPath)
			found += subFound
			imported += subImported
			skipped += subSkipped
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !isMediaExt(ext) {
			continue
		}

		fullPath := filepath.Join(watchPath, entry.Name())
		found++

		if m.isAlreadyImported(fullPath) {
			skipped++
			continue
		}

		result := m.importFile(fullPath, entry.Name(), mediaType, libPath)
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
	m.db.QueryRow(`SELECT COUNT(*) FROM imported_files WHERE original_path = ?`, path).Scan(&count)
	m.mu.RUnlock()
	return count > 0
}

// ── File Import ────────────────────────────────────────────────

func (m *Module) importFile(fullPath, fileName, mediaType, libPath string) bool {
	parsed := parseFileName(fileName)
	if parsed.Title == "" {
		slog.Debug("unable to parse filename", "file", fileName)
		return false
	}

	if mediaType != "both" && mediaType != "" && parsed.MediaType != mediaType {
		slog.Debug("media type mismatch", "file", fileName, "detected", parsed.MediaType, "expected", mediaType)
		return false
	}

	destPath := m.buildDestPath(parsed, libPath)
	if destPath == "" {
		slog.Debug("unable to build destination path", "file", fileName)
		return false
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		slog.Error("create destination directory", "path", destDir, "error", err)
		return false
	}

	if err := os.Rename(fullPath, destPath); err != nil {
		slog.Error("move file", "src", fullPath, "dst", destPath, "error", err)
		return false
	}

	now := time.Now().UTC().Format(time.RFC3339)
	importID := fmt.Sprintf("imp_%d", time.Now().UnixNano())

	m.mu.Lock()
	m.db.Exec(`INSERT INTO imported_files (id, original_path, destination_path, file_name, media_type, title, year, season_number, episode_number, quality, tmdb_id, imported_at, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'imported')`,
		importID, fullPath, destPath, fileName, parsed.MediaType, parsed.Title, parsed.Year, parsed.Season, parsed.Episode, parsed.Quality, parsed.TMDBID, now,
	)
	m.mu.Unlock()

	slog.Info("imported file", "src", fileName, "dst", destPath, "type", parsed.MediaType, "title", parsed.Title)

	go m.publish(context.Background(), contracts.EventFileImported, map[string]interface{}{
		"original_path":    fullPath,
		"destination_path": destPath,
		"media_type":       parsed.MediaType,
		"title":            parsed.Title,
		"year":             parsed.Year,
		"season_number":    parsed.Season,
		"episode_number":   parsed.Episode,
		"quality":          parsed.Quality,
	})

	return true
}

func (m *Module) buildDestPath(p parsedFile, libPath string) string {
	root := libPath
	if root == "" {
		root = m.libraryRoot
	}
	safeTitle := sanitizeName(p.Title)
	ext := filepath.Ext(p.FileName)

	switch p.MediaType {
	case "movie":
		yearStr := ""
		if p.Year > 0 {
			yearStr = fmt.Sprintf(" (%d)", p.Year)
		}
		dir := filepath.Join(root, "Movies", safeTitle+yearStr)
		base := fmt.Sprintf("%s.%d%s", safeTitle, p.Year, ext)
		if p.Quality != "" {
			base = fmt.Sprintf("%s.%d.%s%s", safeTitle, p.Year, p.Quality, ext)
		}
		return filepath.Join(dir, base)

	case "tv":
		seasonDir := fmt.Sprintf("Season %02d", p.Season)
		epBase := fmt.Sprintf("%s.S%02dE%02d", safeTitle, p.Season, p.Episode)
		if p.Quality != "" {
			epBase = fmt.Sprintf("%s.S%02dE%02d.%s", safeTitle, p.Season, p.Episode, p.Quality)
		}
		dir := filepath.Join(root, "TV", safeTitle, seasonDir)
		return filepath.Join(dir, epBase+ext)

	default:
		return filepath.Join(root, "Other", safeTitle, p.FileName)
	}
}

// ── Filename Parsing ───────────────────────────────────────────

var (
	reMovie = regexp.MustCompile(`(?i)^(.+?)[.\s-_]+(\d{4})(?:[.\s-_]+(.+))?\.\w+$`)

	reTVSeasonEpisode = regexp.MustCompile(`(?i)[.\s-_]+S(\d{1,2})E(\d{1,2})[.\s-_]+`)
	reTVSeasonEpLong  = regexp.MustCompile(`(?i)[.\s-_]+(\d{1,2})x(\d{1,2})[.\s-_]+`)
	reTVSeasonOnly    = regexp.MustCompile(`(?i)[.\s-_]+Season[.\s-_]+(\d{1,2})[.\s-_]+`)
	reTVEpisodeOnly   = regexp.MustCompile(`(?i)[.\s-_]+Episode[.\s-_]+(\d{1,2})[.\s-_]+`)

	reQuality = regexp.MustCompile(`(?i)(\d{3,4}[pi]|4k|uhd)[.\s-_]*(remux|bluray|brrip|bdrip|blu-ray|web[-\s]?dl|webdl|webrip|hdtv)?`)

	reCleanTitle = regexp.MustCompile(`[.\s-_]+`)
)

type parsedFile struct {
	FileName  string
	Title     string
	Year      int
	Season    int
	Episode   int
	Quality   string
	MediaType string
	TMDBID    int
}

func parseFileName(fileName string) parsedFile {
	result := parsedFile{FileName: fileName}

	if m := reTVSeasonEpisode.FindStringSubmatch(fileName); len(m) >= 3 {
		result.MediaType = "tv"
		result.Season, _ = strconv.Atoi(m[1])
		result.Episode, _ = strconv.Atoi(m[2])
		result.Title = extractTVTitle(fileName, m[0])
	} else if m := reTVSeasonEpLong.FindStringSubmatch(fileName); len(m) >= 3 {
		result.MediaType = "tv"
		result.Season, _ = strconv.Atoi(m[1])
		result.Episode, _ = strconv.Atoi(m[2])
		result.Title = extractTVTitle(fileName, m[0])
	} else if reTVSeasonOnly.MatchString(fileName) || reTVEpisodeOnly.MatchString(fileName) {
		result.MediaType = "tv"
		if m := reTVSeasonOnly.FindStringSubmatch(fileName); len(m) >= 2 {
			result.Season, _ = strconv.Atoi(m[1])
		}
		if m := reTVEpisodeOnly.FindStringSubmatch(fileName); len(m) >= 2 {
			result.Episode, _ = strconv.Atoi(m[1])
		}
		result.Title = extractTVTitle(fileName, "")
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

	result.Quality = extractQuality(fileName)
	return result
}

func extractTVTitle(fileName, match string) string {
	s := fileName
	ext := filepath.Ext(s)
	s = s[:len(s)-len(ext)]

	if match != "" {
		parts := strings.SplitN(s, match, 2)
		if len(parts) > 0 {
			return cleanTitle(parts[0])
		}
	}

	s = reTVSeasonEpisode.ReplaceAllString(s, "")
	s = reTVSeasonEpLong.ReplaceAllString(s, "")
	s = reTVSeasonOnly.ReplaceAllString(s, "")
	s = reTVEpisodeOnly.ReplaceAllString(s, "")
	return cleanTitle(s)
}

func extractQuality(name string) string {
	m := reQuality.FindString(name)
	return strings.TrimSpace(m)
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

// ── gRPC API ───────────────────────────────────────────────────

func (m *Module) Scan(ctx context.Context, req *scannerv1.ScanRequest) (*scannerv1.ScanResponse, error) {
	dirs := m.collectWatchDirs()
	if dirs == nil {
		return nil, fmt.Errorf("not initialized")
	}

	var totalFound, totalImported, totalSkipped int
	for _, d := range dirs {
		found, imported, skipped := m.scanDirectory(d.path, d.mediaType, d.libPath)
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
	db.QueryRow(`SELECT started_at, status FROM scan_log ORDER BY started_at DESC LIMIT 1`).Scan(&lastStatus, &lastScanAt)

	return &scannerv1.GetStatsResponse{
		TotalImported:  int32(totalImported),
		WatchDirs:      int32(watchDirs),
		LastScanStatus: lastStatus,
	}, nil
}

func (m *Module) AddWatchDir(ctx context.Context, req *scannerv1.AddWatchDirRequest) (*scannerv1.AddWatchDirResponse, error) {
	if req.GetPath() == "" {
		return nil, fmt.Errorf("path is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	mediaType := req.GetMediaType()
	if mediaType == "" {
		mediaType = "both"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := fmt.Sprintf("wd_%d", time.Now().UnixNano())

	_, err := m.db.Exec(`INSERT OR IGNORE INTO watch_dirs (id, path, media_type, library_path, enabled, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		id, req.GetPath(), mediaType, req.GetLibraryPath(), now)
	if err != nil {
		return nil, fmt.Errorf("insert watch dir: %w", err)
	}

	slog.Info("added watch directory", "path", req.GetPath(), "type", mediaType)
	return &scannerv1.AddWatchDirResponse{Id: id}, nil
}

func (m *Module) RemoveWatchDir(ctx context.Context, req *scannerv1.RemoveWatchDirRequest) (*scannerv1.RemoveWatchDirResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	_, err := m.db.Exec(`DELETE FROM watch_dirs WHERE id = ?`, req.GetId())
	if err != nil {
		return nil, fmt.Errorf("delete watch dir: %w", err)
	}
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

var _ contracts.Module = (*Module)(nil)
