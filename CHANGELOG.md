# Changelog

## [0.1.32] - 2026-10-05


### Security
- gRPC server and peer dials use mesh TLS (meshtls, sdk/go/module v0.6.5) unless the dev insecure flag is set (ADR-0016/0017).

## [0.1.31] - 2026-10-05

### Changed
- Built on core v0.6.14 / sdk/go/module v0.6.4: unregisters on shutdown and re-registers after core restarts (ADR-0022).

## [0.1.30] - 2026-10-05


### Changed
- Reported version comes from muxcore.json (ADR-0021); built on core v0.6.12 / sdk/go/module v0.6.3 (mesh enrollment, ADR-0017).

## [0.1.29] - 2026-10-05


### Fixed
- Rename previews are now requested with a relative `FilePath` (the file name), so media-rename returns a library-relative `NewPath` and the household naming template applies to imports.
- Imports no longer nest media-rename's absolute `Preview.NewPath` under the library root (e.g. `library/Movies/home/.../Fight Club (1999)/...`). Absolute preview paths are used only when inside the intended library root, relative ones are joined under it, and anything escaping (`..` or outside the root) falls back to the scanner's own naming.

## [0.1.28] - 2026-10-05


### Added
- Upgrade test (`internal/upgrade_test.go`, ADR-0015 / NFR-DATA-002) opening the committed v0.1.9 database snapshot (`internal/testdata/upgrade/`) with the current code twice and checking schema superset, seeded-row read-back, new-column defaults, and integrity.

## [0.1.27] — 2026-09-08

### Added
- `UpdateWatchDir` and `SetWatchDirEnabled` so household Settings can pause or retarget a watch folder without delete-and-re-add.
- Persist `tv_library_path` / `music_library_path` from `AddWatchDir` and return them on `ListWatchDirs`.

## [0.1.26] — 2026-08-22

### Fixed
- `ImportPath` treats already-imported files as success (`FilesImported=1`) so automation retries stop failing on idempotent re-imports.
- `ImportPath` honors caller context cancellation and deadlines during directory walks and ffprobe quality probes.
- Paths outside registered watch directories, and unresolved relative paths, include the configured watch roots in the error message.
- Single-file import of rejected or unrecognized media returns an error instead of a silent skip response.
- Directory and `storage://` imports return an error when media files are found but none are imported.

### Changed
- Import-path validation and error formatting extracted to `internal/import_path.go`.

## [0.1.23] — 2026-08-20

### Added
- `ImportPath` accepts `storage://…` URIs (mesh StorageService). Lists `torrent/{ih}/files/` when given a prefix and streams objects into the library dest — no local DOWNLOAD_DIR watch required for mesh-backed torrents.

## [0.1.22] — 2026-08-20

### Added
- `ListImportCandidates` — list unimported media files under enabled watch dirs for admin manual import.

## [0.1.21] — 2026-08-18

### Fixed
- Mesh `storage.Put` is off unless `SCANNER_USE_MESH_STORAGE=true`. Local library copy/link is the default, so vault imports no longer EOF on Put and fall back. Sidecar subtitles copy locally when Put is disabled or fails.

## [0.1.20] — 2026-08-18

### Fixed
- A better-quality TV import replaces the worse file for the same episode (e.g. 1080p replaces 900p). Equal or worse copies are skipped so the library keeps one file per episode.

## [0.1.19] — 2026-08-18

### Fixed
- A downloads watch dir stores both movie (`library_path`) and TV (`tv_library_path`) dest roots. TV files never resolve under a `movies/` library path (they go to `SCANNER_TV_LIBRARY_ROOT`, or a sibling `shows/` folder).

## [0.1.18] — 2026-08-18

### Fixed
- Numbered episode files (`001.mp4`) in a season-pack folder inherit the series title and go to the TV library instead of `movies/Other`.
- Junk titles (`RARBG`, proofs/screens folders) are not imported as shows.
- TV dest folders reuse an existing series directory case-insensitively so `King Of The Hill` does not create a second show.

## [0.1.17] — 2026-08-18

### Added
- `ImportPath` logs relative → absolute resolution at Info (`from` / `to`, or unresolved).

## [0.1.16] — 2026-08-18

### Fixed
- `ImportPath` accepts files under `{cwd}/partials` and `{watchDir}/partials` so relative save paths from `keep_stalled_partials` still import when the downloader wrote next to the process cwd instead of the downloads watch dir.

## [0.1.15] — 2026-08-18

### Fixed
- `ImportPath` resolves relative paths (`partials/{item}/…`) against registered watch directories so completed torrents with a relative save path still import.

## [0.1.14] — 2026-08-18

### Fixed
- Skip mesh `storage.Put` when a local library destination path is known. Put was EOF'ing on every vault import (`send: EOF`) and then falling back to copy anyway.

## [0.1.13] — 2026-08-18

### Fixed
- Do not import **extras / bonus / featurettes / alternate scenes** as the main episode. Breaking Bad S04 extras were overwriting `S04E01 Box Cutter` in the library.
- Skip `Extras/` (and similar) folders in the download watch scan.
- Do not replace an existing library file with a **smaller** source.

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

## [0.1.27] - 2026-10-05

### Changed
- CI runs on GitHub-hosted runners from the umbrella template; retired-origin workflows removed.
- Dependencies resolve from published GitHub tags (no filesystem `replace`); requires core v0.6.0.

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
