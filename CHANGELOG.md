# Changelog

## v0.1.0 (2026-06-14)

- Initial release
- Directory scanning with periodic (60s) watch loop
- Filename parsing for movies (Title.Year.Quality) and TV (Title.SxxExx.Quality)
- File organization: Movies/{Title (Year)}/ and TV/{Title}/Season XX/
- SQLite tracking of imported files, watch dirs, and scan history
- Deduplication — already-imported files are skipped
- gRPC API: Scan, AddWatchDir, RemoveWatchDir, ListWatchDirs, ListImported, GetStats
