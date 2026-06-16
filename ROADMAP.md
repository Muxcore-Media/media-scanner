# Roadmap

## v0.1.0 (Current)

- [x] Directory scanning with 60s periodic loop
- [x] Filename parsing (movie + TV SxxExx)
- [x] File organization into Movies/ and TV/ directories
- [x] SQLite tracking (imported files, watch dirs, scan history)
- [x] gRPC management API

## v0.2.0 (Planned)

- [ ] Library module integration — update media-movies/media-tvshows on import
- [ ] Quality detection from file metadata (ffprobe integration)
- [ ] Hardlink support (instead of move)
- [ ] Sample file detection and rejection
- [ ] Subtitle file import alongside video
- [ ] Event publishing on import complete

## Future

- [ ] fsnotify/inotify-based real-time watching
- [ ] Multiple naming convention templates
- [ ] Extraction of archives (rar, zip) before scanning
