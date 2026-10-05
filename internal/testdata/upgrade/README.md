# Upgrade snapshots (ADR-0015)

- `v0.1.9.db` / `v0.1.9.schema.sql`: database produced by media-scanner v0.1.9
  (the previous release-train pin; also the tag before the latest, v0.1.27).
- Produced by `seed_upgrade_test.go.txt` (build tag `upgradeseed`), copied into a
  `git worktree` of tag v0.1.9 as `internal/seed_upgrade_test.go` and run with
  `UPGRADE_SEED_DB=/tmp/x.db go test -tags upgradeseed -run TestUpgradeSeed ./internal/`,
  then `sqlite3 x.db 'PRAGMA wal_checkpoint(TRUNCATE); VACUUM; PRAGMA journal_mode=DELETE;'`.
  Old-tag dependency drift was handled with a throwaway `-modfile` and `GOSUMDB=off`.
- Seed: `watch_dirs` 3 rows (movie/tv/music, one disabled, home-directory paths),
  `imported_files` 3 rows (movie, tv episode, failed import with zeroed metadata),
  `scan_log` 2 rows (one completed, one running with NULL `completed_at`).
- `internal/upgrade_test.go` opens each snapshot with the current `Module.Init`.
