CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS categories (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  last_seen_scan_id INTEGER,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS albums (
  id INTEGER PRIMARY KEY,
  category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  last_seen_scan_id INTEGER,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(category_id, name)
);

CREATE TABLE IF NOT EXISTS photos (
  id INTEGER PRIMARY KEY,
  album_id INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
  filename TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  file_size INTEGER NOT NULL,
  file_mtime_ns INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  width INTEGER,
  height INTEGER,
  taken_at TEXT,
  thumbnail_key TEXT,
  last_seen_scan_id INTEGER,
  indexed_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(album_id, filename)
);

CREATE TABLE IF NOT EXISTS scan_runs (
  id INTEGER PRIMARY KEY,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed')),
  files_seen INTEGER NOT NULL DEFAULT 0,
  files_new INTEGER NOT NULL DEFAULT 0,
  files_changed INTEGER NOT NULL DEFAULT 0,
  files_unchanged INTEGER NOT NULL DEFAULT 0,
  files_removed INTEGER NOT NULL DEFAULT 0,
  warnings_count INTEGER NOT NULL DEFAULT 0,
  errors_count INTEGER NOT NULL DEFAULT 0,
  error_summary TEXT
);

CREATE INDEX IF NOT EXISTS idx_photos_album_taken
  ON photos(album_id, taken_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_photos_taken
  ON photos(taken_at DESC, id DESC)
  WHERE taken_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_albums_category_name
  ON albums(category_id, name);
