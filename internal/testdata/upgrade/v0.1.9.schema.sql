CREATE TABLE watch_dirs (
			id           TEXT PRIMARY KEY,
			path         TEXT NOT NULL UNIQUE,
			media_type   TEXT NOT NULL DEFAULT 'both',
			library_path TEXT NOT NULL DEFAULT '',
			enabled      INTEGER DEFAULT 1,
			created_at   TEXT NOT NULL
		);
CREATE TABLE imported_files (
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
		);
CREATE TABLE scan_log (
			id             TEXT PRIMARY KEY,
			started_at     TEXT NOT NULL,
			completed_at   TEXT,
			files_found    INTEGER DEFAULT 0,
			files_imported INTEGER DEFAULT 0,
			files_skipped  INTEGER DEFAULT 0,
			status         TEXT DEFAULT 'running'
		);
CREATE INDEX idx_imported_type ON imported_files(media_type, status)
	;
