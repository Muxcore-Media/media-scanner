package internal

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	reMusicTrackNum      = regexp.MustCompile(`^(\d{1,2})[.\s_-]+(.+)$`)
	musicLibraryDirNames = []string{"music", "Music"}
)

func isAudioExt(ext string) bool {
	switch ext {
	case ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".wma":
		return true
	}
	return false
}

func isImportableExt(ext string) bool {
	return isMediaExt(ext) || isAudioExt(ext)
}

func (m *Module) musicLibraryRootFor(libPath string) string {
	if v := strings.TrimSpace(m.musicLibraryRoot); v != "" {
		return v
	}
	if root := strings.TrimSpace(libPath); root != "" {
		return filepath.Join(filepath.Dir(filepath.Clean(root)), "music")
	}
	return filepath.Join(m.libraryRoot, "music")
}

func parseMusicFile(fileName, fullPath, musicRoot string) parsedFile {
	result := parsedFile{
		FileName:  fileName,
		MediaType: "music",
		Quality:   extractQuality(fileName),
	}
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	base = strings.TrimSpace(base)

	if artist, album, track, ok := parseMusicFromRelPath(fullPath, musicRoot); ok {
		result.Artist, result.Album, result.Title = artist, album, track
		if result.Quality == "" {
			result.Quality = extractQuality(fileName)
		}
		return result
	}

	parts := splitMusicTitle(base)
	switch len(parts) {
	case 3:
		result.Artist, result.Album, result.Title = parts[0], parts[1], parts[2]
	case 2:
		result.Artist, result.Title = parts[0], parts[1]
		result.Album = "Unknown Album"
	default:
		result.Title = cleanTitle(base)
		if result.Title == "" {
			result.Title = base
		}
		result.Artist = "Unknown Artist"
		result.Album = "Unknown Album"
	}
	result.Title = stripTrackNumber(result.Title)
	if result.Artist == "" {
		result.Artist = "Unknown Artist"
	}
	if result.Album == "" {
		result.Album = "Unknown Album"
	}
	return result
}

func splitMusicTitle(s string) []string {
	s = strings.ReplaceAll(s, "_", " ")
	for _, sep := range []string{" - ", " – ", " — ", " / "} {
		if strings.Contains(s, sep) {
			parts := strings.Split(s, sep)
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					out = append(out, p)
				}
			}
			return out
		}
	}
	return nil
}

func stripTrackNumber(name string) string {
	name = strings.TrimSpace(name)
	if m := reMusicTrackNum.FindStringSubmatch(name); len(m) >= 3 {
		return strings.TrimSpace(m[2])
	}
	return name
}

func parseMusicFromRelPath(absPath, root string) (artist, album, track string, ok bool) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", "", "", false
	}
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return "", "", "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", "", "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 {
		return "", "", "", false
	}
	base := parts[len(parts)-1]
	track = stripTrackNumber(strings.TrimSuffix(base, filepath.Ext(base)))
	switch len(parts) {
	case 2:
		return parts[0], "Unknown Album", track, track != ""
	case 3:
		return parts[0], parts[1], track, track != ""
	default:
		return parts[0], parts[1], track, track != ""
	}
}

func (m *Module) importMusicFile(fullPath, fileName, recordPath, mediaType, libPath string) bool {
	if recordPath == "" {
		recordPath = fullPath
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if !isAudioExt(ext) {
		return false
	}
	var size int64
	if info, err := os.Stat(fullPath); err == nil {
		size = info.Size()
	}
	if reason, reject := m.isSampleAudioFile(fileName, size); reject {
		slog.Info("skipping junk audio file", "file", fileName, "reason", reason)
		return false
	}

	musicRoot := m.musicLibraryRootFor(libPath)
	parsed := parseMusicFile(fileName, fullPath, musicRoot)
	if mediaType != "" && mediaType != "both" && mediaType != "music" && mediaType != "audio" {
		slog.Debug("music type mismatch", "file", fileName, "expected", mediaType)
		return false
	}
	if parsed.Title == "" || isJunkImportTitle(parsed.Title) {
		slog.Info("skipping unrecognized music", "file", fileName)
		return false
	}

	storageKey, destPath := m.resolveImportPaths(parsed, libPath, "", fullPath)
	if storageKey == "" || destPath == "" {
		return false
	}

	if destKeepExisting(destPath, fullPath) {
		m.recordImported(recordPath, destPath, fileName, parsed, parsed.Quality)
		return true
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		slog.Error("create music destination directory", "path", destDir, "error", err)
		m.failImport(context.Background(), recordPath, err.Error())
		return false
	}
	if err := placeFile(fullPath, destPath, m.importMode); err != nil {
		slog.Error("place music file", "src", fullPath, "dst", destPath, "error", err)
		m.failImport(context.Background(), recordPath, err.Error())
		return false
	}
	if m.importMode == "move" {
		_ = os.Remove(fullPath)
	}

	m.recordImported(recordPath, destPath, fileName, parsed, parsed.Quality)
	slog.Info("imported music file", "src", fileName, "dest", destPath, "artist", parsed.Artist, "album", parsed.Album)

	go m.publishFileImported(context.Background(), fileImportedPayloadFromParsed(recordPath, destPath, storageKey, parsed.Quality, parsed))
	return true
}

func (m *Module) isSampleAudioFile(name string, size int64) (reason string, reject bool) {
	if size == 0 {
		return "empty file", true
	}
	if strings.HasSuffix(strings.ToLower(name), ".part") {
		return "incomplete download", true
	}
	if reExtraName.MatchString(name) {
		return "extra/bonus filename", true
	}
	return "", false
}

func buildMusicDestPath(p parsedFile, root string) string {
	safeArtist := sanitizeName(p.Artist)
	safeAlbum := sanitizeName(p.Album)
	safeTrack := sanitizeName(p.Title)
	ext := filepath.Ext(p.FileName)
	if safeTrack == "" {
		safeTrack = strings.TrimSuffix(p.FileName, ext)
	}
	base := safeTrack + ext
	if p.Quality != "" {
		base = safeTrack + "." + p.Quality + ext
	}
	folder := libraryKindPrefix(root, "music")
	join := func(elem ...string) string {
		parts := []string{root}
		if folder != "" {
			parts = append(parts, folder)
		}
		parts = append(parts, elem...)
		return filepath.Join(parts...)
	}
	return join(safeArtist, safeAlbum, base)
}

func buildMusicStorageKey(p parsedFile) string {
	safeArtist := sanitizeName(p.Artist)
	safeAlbum := sanitizeName(p.Album)
	safeTrack := sanitizeName(p.Title)
	ext := filepath.Ext(p.FileName)
	if safeTrack == "" {
		safeTrack = strings.TrimSuffix(p.FileName, ext)
	}
	base := safeTrack + ext
	if p.Quality != "" {
		base = safeTrack + "." + p.Quality + ext
	}
	return filepath.Join("media", "Music", safeArtist, safeAlbum, base)
}
