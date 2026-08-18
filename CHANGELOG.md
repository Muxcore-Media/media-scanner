# Changelog

## [0.1.12] — 2026-08-18

### Fixed
- Ignore `*.part` / temp writes in the download watch dir so in-progress torrents do not trigger a full rescan every few seconds.
- Default fsnotify debounce 400ms → 5s.

## [0.1.11] — 2026-08-18

### Fixed
- `ImportPath` of a torrent file no longer imports sidecar junk (`.txt`, `.nfo`, etc.); media-extension check applies to single-file imports too.

## [0.1.10] — 2026-08-18

### Fixed
- `ImportPath` on a file imports **only that file**, not the whole parent downloads directory.
- Skip storage put / recopy when the library destination already exists at the same size; record it as imported so the watch loop stops retrying.
- 15-minute cooldown after `storage.Put` failures so a huge remux EOF does not hammer core gRPC.

### Changed
- TV/movie dest paths honor library root folder names (no extra `Movies/`/`TV/` prefix when the root is already that folder).
- Filename parse: `Sxx Eyy`, leading `E01 Title`, season-code packs, dash episode numbers.

## [0.1.9] — 2026-08-10

### Added

- Advertise `settings` capability so admin-ui discovers SettingsProvider without ListAll probing.

## [0.1.8] — 2026-08-10

### Added
- SettingsProvider mesh (`RegisterSettings`) for import mode, library root, size filters, and safety rescan.

### Changed
- Safety rescan loop re-reads interval so live setting updates apply without restart.
- Pin `core/sdk/go/module` to **v0.5.2**.


## [0.1.7] — 2026-08-10

### Fixed
- Align Info()/muxcore.json version to **0.1.7** (v0.1.6 tag still advertised 0.1.5).


## [0.1.6] — 2026-08-10

### Fixed
- Record absolute library destination_path for imports (keep storage_key separate).


## [0.1.5] — 2026-08-10

### Fixed
- Sync Info()/muxcore.json version to **0.1.5**.


## [0.1.4] — 2026-08-10

### Fixed
- Module `Info().Version` aligned to **0.1.4** (was 0.1.0).

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
