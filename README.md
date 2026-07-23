# Media Scanner

[![CI](https://github.com/Muxcore-Media/media-scanner/actions/workflows/ci.yml/badge.svg)](https://github.com/Muxcore-Media/media-scanner/actions)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

**Scans download directories, identifies media files via filename parsing, and imports them into an organized library structure.**

A MuxCore sidecar module that watches download directories for new media files, parses their filenames to identify movies and TV shows, and organizes them into a clean library hierarchy. When connected to core, imports go through the storage API; otherwise files are hardlinked/copied/moved on the local filesystem.

---

## How It Works

```
Download dir ──→ media-scanner ──→ Organized library
                   │
                   ├── Movies/Title (Year)/Title.Year.Quality.mkv
                   └── TV/Show Name/Season XX/Show.Name.SXXEYY.Quality.mkv
```

### Key Features

- **Automatic scanning** — fsnotify watches download directories (plus a 15m safety rescan)
- **Archive extract** — ZIP/RAR archives are unpacked before import
- **Filename parsing** — extracts title, year, season, episode, and quality from release names
- **Smart organization** — Movies go to `Movies/Title (Year)/`, TV goes to `TV/Title/Season XX/`
- **Naming templates** — when `media.roots` / `media.rename` are available, destination paths follow the root’s naming template
- **Library root rescan** — `ScanLibraryRoots` indexes media already under registered library roots (or `SCANNER_LIBRARY_ROOT`)
- **Deduplication** — already-imported files are tracked in SQLite and skipped
- **Sample filtering** — skips sample/trailer names and files below `SCANNER_MIN_VIDEO_BYTES`
- **Sidecar subtitles** — imports matching subtitle files alongside video (registers with `media.subtitles` when available)
- **Watch directory management** — add/remove watched paths at runtime via gRPC; optional `SCANNER_DEFAULT_WATCH_DIR` auto-registers on start
- **Import history** — full audit trail of all imported files
- **Targeted import** — `ImportPath` imports files under a single path (must be inside a watch dir). Register the downloader `DOWNLOAD_DIR` as a watch directory so automation can import completed downloads.

---

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SCANNER_DB_PATH` | `/var/lib/media-scanner/scanner.db` | SQLite database path |
| `SCANNER_GRPC_ADDR` | `:9470` | gRPC listen address |
| `SCANNER_LIBRARY_ROOT` | `/data/media` | Root directory for organized media |
| `SCANNER_DEFAULT_WATCH_DIR` | _(empty)_ | Auto-register this path as a watch dir on start |
| `SCANNER_IMPORT_MODE` | `hardlink` | Local import mode when core storage is unavailable: `hardlink`, `copy`, or `move` |
| `SCANNER_SAMPLE_MAX_BYTES` | `209715200` (200 MiB) | Max size for sample/trailer filename rejection |
| `SCANNER_MIN_VIDEO_BYTES` | `5242880` (5 MiB) | Reject videos smaller than this (`0` disables) |
| `SCANNER_SAFETY_RESCAN` | `15m` | Fallback full rescan interval (`0` disables) |
| `MUXCORE_GRPC_ADDR` | `localhost:9090` | Core mesh gRPC address |
| `MUXCORE_INSECURE_DISABLE_TLS` | `false` | Disable TLS for dev |

---

## Quick Start

```bash
# Build
make build

# Run against local core (dev mode)
export MUXCORE_INSECURE_DISABLE_TLS=true
./media-scanner --muxcore-mesh-addr localhost:9090

# Add a watch directory
grpcurl -d '{"path": "/downloads", "library_path": "/data/media"}' \
  :9470 muxcore.scanner.v1.ScannerService/AddWatchDir

# Trigger a scan
grpcurl :9470 muxcore.scanner.v1.ScannerService/Scan

# Rescan library roots in place
grpcurl :9470 muxcore.scanner.v1.ScannerService/ScanLibraryRoots
```

### gRPC API

`Scan`, `ScanLibraryRoots`, `ImportPath`, `AddWatchDir`, `RemoveWatchDir`, `ListWatchDirs`, `ListImported`, `GetStats`

---

## Development

```bash
make test     # run tests with race detection
make lint     # golangci-lint
make fmt      # format code
make proto    # regenerate protobuf code
```

---

## License

GPL-3.0
