# Media Scanner

[![CI](https://github.com/Muxcore-Media/media-scanner/actions/workflows/ci.yml/badge.svg)](https://github.com/Muxcore-Media/media-scanner/actions)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

**Scans download directories, identifies media files via filename parsing, and imports them into an organized library structure.**

A MuxCore sidecar module that watches download directories for new media files, parses their filenames to identify movies and TV shows, and organizes them into a clean library hierarchy.

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
- **Deduplication** — already-imported files are tracked in SQLite and skipped
- **Watch directory management** — add/remove watched paths at runtime via gRPC
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
| `SCANNER_SAFETY_RESCAN` | `15m` | Fallback full rescan interval (`0` disables) |
| `MUXCORE_GRPC_ADDR` | `localhost:9090` | Core mesh gRPC address |
| `MUXCORE_GRPC_INSECURE` | `false` | Disable TLS for dev |

---

## Quick Start

```bash
# Build
make build

# Run against local core (dev mode)
export MUXCORE_GRPC_INSECURE=true
./media-scanner --muxcore-mesh-addr localhost:9090

# Add a watch directory
grpcurl -d '{"path": "/downloads", "library_path": "/data/media"}' \
  :9470 muxcore.scanner.v1.ScannerService/AddWatchDir

# Trigger a scan
grpcurl :9470 muxcore.scanner.v1.ScannerService/Scan
```

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
