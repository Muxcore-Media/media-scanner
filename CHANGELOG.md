# Changelog

## Unreleased

- Replace 60s poll with fsnotify watching and a configurable safety rescan (`SCANNER_SAFETY_RESCAN`, default 15m)
- Extract ZIP/RAR archives into the watch tree before import (zip-slip safe; password archives skipped)
- `ImportPath` RPC — targeted scan/import of a path under a registered watch dir (used by media-automation on download complete)
- `ScanLibraryRoots` RPC — index media already under registered library roots / `SCANNER_LIBRARY_ROOT`
- Honor `media.roots` naming templates via `media.rename` on import destination paths
- Import via core storage API when mesh-connected; local `hardlink`/`copy`/`move` fallback (`SCANNER_IMPORT_MODE`)
- Auto-register `SCANNER_DEFAULT_WATCH_DIR`; sample/junk filtering (`SCANNER_SAMPLE_MAX_BYTES`, `SCANNER_MIN_VIDEO_BYTES`)
- Sidecar subtitle import (+ optional `media.subtitles` registration / embedded detect)
- Capability `media.scanner` for discovery

## v0.1.0 (2026-06-14)

- Initial release
- Directory scanning with periodic (60s) watch loop
- Filename parsing for movies (Title.Year.Quality) and TV (Title.SxxExx.Quality)
- File organization: Movies/{Title (Year)}/ and TV/{Title}/Season XX/
- SQLite tracking of imported files, watch dirs, and scan history
- Deduplication — already-imported files are skipped
- gRPC API: Scan, AddWatchDir, RemoveWatchDir, ListWatchDirs, ListImported, GetStats
