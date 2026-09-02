package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const timeFormat = "2006-01-02T15:04:05Z07:00"

type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	conn dbtx
	// TxHook, if set, is invoked once after each successful InTx commit.
	// Intended for tests to count batch boundaries.
	TxHook func()
}

func NewStore(db *sql.DB) *Store {
	return &Store{conn: db}
}

// RawDB returns the underlying *sql.DB for callers that need direct access
// (mainly reconciliation and tests). Returns nil when Store is tx-scoped.
func (s *Store) RawDB() *sql.DB {
	db, _ := s.conn.(*sql.DB)
	return db
}

// InTx runs fn inside a short transaction and rolls back on error or panic.
// Nested calls are rejected: the indexer batches at exactly one level.
func (s *Store) InTx(ctx context.Context, fn func(*Store) error) (err error) {
	db, ok := s.conn.(*sql.DB)
	if !ok {
		return errors.New("catalog: nested transaction not supported")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
		if err == nil && s.TxHook != nil {
			s.TxHook()
		}
	}()
	err = fn(&Store{conn: tx})
	return err
}

func fmtTime(t time.Time) string { return t.UTC().Format(timeFormat) }

// StartScan marks any stale running rows as failed then inserts a new running row.
func (s *Store) StartScan(ctx context.Context, now time.Time) (int64, error) {
	nowStr := fmtTime(now)
	if _, err := s.conn.ExecContext(ctx,
		`UPDATE scan_runs SET status='failed', finished_at=?, error_summary='abandoned by next run' WHERE status='running'`,
		nowStr,
	); err != nil {
		return 0, fmt.Errorf("abandon running scans: %w", err)
	}
	var id int64
	err := s.conn.QueryRowContext(ctx,
		`INSERT INTO scan_runs (started_at, status) VALUES (?, 'running') RETURNING id`,
		nowStr,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert scan_run: %w", err)
	}
	return id, nil
}

// FinishScan sets the terminal state and counters for a scan run.
func (s *Store) FinishScan(ctx context.Context, scanID int64, status string, counts ScanCounts, errSummary string, now time.Time) error {
	if status != "succeeded" && status != "failed" {
		return fmt.Errorf("invalid finish status: %q", status)
	}
	var summary sql.NullString
	if errSummary != "" {
		summary = sql.NullString{String: errSummary, Valid: true}
	}
	_, err := s.conn.ExecContext(ctx,
		`UPDATE scan_runs SET finished_at=?, status=?, files_seen=?, files_new=?, files_changed=?, files_unchanged=?, files_removed=?, warnings_count=?, errors_count=?, error_summary=? WHERE id=?`,
		fmtTime(now), status,
		counts.Seen, counts.New, counts.Changed, counts.Unchanged, counts.Removed,
		counts.Warnings, counts.Errors,
		summary, scanID,
	)
	return err
}

// UpsertCategory inserts or updates a category by relative_path, returning the id.
func (s *Store) UpsertCategory(ctx context.Context, name, relPath string, scanID int64, now time.Time) (int64, error) {
	nowStr := fmtTime(now)
	var id int64
	err := s.conn.QueryRowContext(ctx,
		`INSERT INTO categories (name, relative_path, last_seen_scan_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(relative_path) DO UPDATE SET
		   name=excluded.name,
		   last_seen_scan_id=excluded.last_seen_scan_id,
		   updated_at=excluded.updated_at
		 RETURNING id`,
		name, relPath, scanID, nowStr, nowStr,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert category %q: %w", relPath, err)
	}
	return id, nil
}

// UpsertAlbum inserts or updates an album by relative_path, returning the id.
func (s *Store) UpsertAlbum(ctx context.Context, categoryID int64, name, relPath string, scanID int64, now time.Time) (int64, error) {
	nowStr := fmtTime(now)
	var id int64
	err := s.conn.QueryRowContext(ctx,
		`INSERT INTO albums (category_id, name, relative_path, last_seen_scan_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(relative_path) DO UPDATE SET
		   category_id=excluded.category_id,
		   name=excluded.name,
		   last_seen_scan_id=excluded.last_seen_scan_id,
		   updated_at=excluded.updated_at
		 RETURNING id`,
		categoryID, name, relPath, scanID, nowStr, nowStr,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert album %q: %w", relPath, err)
	}
	return id, nil
}

// FindPhoto returns the Photo row matching relPath, ok=false when absent.
func (s *Store) FindPhoto(ctx context.Context, relPath string) (Photo, bool, error) {
	var p Photo
	var width, height sql.NullInt64
	var lastSeen sql.NullInt64
	err := s.conn.QueryRowContext(ctx,
		`SELECT id, album_id, filename, relative_path, file_size, file_mtime_ns, mime_type,
		        width, height, taken_at, thumbnail_key, last_seen_scan_id
		 FROM photos WHERE relative_path=?`,
		relPath,
	).Scan(
		&p.ID, &p.AlbumID, &p.Filename, &p.RelativePath, &p.FileSize, &p.FileMTimeNS, &p.MIMEType,
		&width, &height, &p.TakenAt, &p.ThumbnailKey, &lastSeen,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Photo{}, false, nil
	}
	if err != nil {
		return Photo{}, false, err
	}
	if width.Valid {
		p.Width = int(width.Int64)
	}
	if height.Valid {
		p.Height = int(height.Int64)
	}
	if lastSeen.Valid {
		p.LastSeenScanID = lastSeen.Int64
	}
	return p, true, nil
}

// InsertPhoto creates a new photo row, returning the assigned id.
func (s *Store) InsertPhoto(ctx context.Context, p Photo, scanID int64, now time.Time) (int64, error) {
	nowStr := fmtTime(now)
	var id int64
	err := s.conn.QueryRowContext(ctx,
		`INSERT INTO photos (album_id, filename, relative_path, file_size, file_mtime_ns, mime_type,
		                     width, height, taken_at, thumbnail_key, last_seen_scan_id,
		                     indexed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id`,
		p.AlbumID, p.Filename, p.RelativePath, p.FileSize, p.FileMTimeNS, p.MIMEType,
		nullableInt(p.Width), nullableInt(p.Height), p.TakenAt, p.ThumbnailKey, scanID,
		nowStr, nowStr, nowStr,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert photo %q: %w", p.RelativePath, err)
	}
	return id, nil
}

// UpdatePhoto rewrites the mutable fields of an existing photo row.
func (s *Store) UpdatePhoto(ctx context.Context, p Photo, scanID int64, now time.Time) error {
	nowStr := fmtTime(now)
	_, err := s.conn.ExecContext(ctx,
		`UPDATE photos SET file_size=?, file_mtime_ns=?, mime_type=?, width=?, height=?,
		                    taken_at=?, thumbnail_key=?, last_seen_scan_id=?,
		                    indexed_at=?, updated_at=?
		 WHERE id=?`,
		p.FileSize, p.FileMTimeNS, p.MIMEType, nullableInt(p.Width), nullableInt(p.Height),
		p.TakenAt, p.ThumbnailKey, scanID,
		nowStr, nowStr, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update photo %d: %w", p.ID, err)
	}
	return nil
}

// MarkPhotoSeen updates last_seen_scan_id without touching identity fields.
func (s *Store) MarkPhotoSeen(ctx context.Context, photoID, scanID int64, now time.Time) error {
	nowStr := fmtTime(now)
	_, err := s.conn.ExecContext(ctx,
		`UPDATE photos SET last_seen_scan_id=?, updated_at=? WHERE id=?`,
		scanID, nowStr, photoID,
	)
	if err != nil {
		return fmt.Errorf("mark photo seen %d: %w", photoID, err)
	}
	return nil
}

// UpdateThumbnailKey rewrites only the thumbnail_key column.
func (s *Store) UpdateThumbnailKey(ctx context.Context, photoID int64, key sql.NullString, now time.Time) error {
	nowStr := fmtTime(now)
	_, err := s.conn.ExecContext(ctx,
		`UPDATE photos SET thumbnail_key=?, updated_at=? WHERE id=?`,
		key, nowStr, photoID,
	)
	return err
}

// ListUnseenPhotos returns photos not marked seen by scanID (useful for debugging).
func (s *Store) ListUnseenPhotos(ctx context.Context, scanID int64) ([]Photo, error) {
	rows, err := s.conn.QueryContext(ctx,
		`SELECT id, album_id, filename, relative_path, file_size, file_mtime_ns, mime_type,
		        width, height, taken_at, thumbnail_key, last_seen_scan_id
		 FROM photos
		 WHERE last_seen_scan_id IS NULL OR last_seen_scan_id != ?`,
		scanID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		var p Photo
		var width, height, lastSeen sql.NullInt64
		if err := rows.Scan(
			&p.ID, &p.AlbumID, &p.Filename, &p.RelativePath, &p.FileSize, &p.FileMTimeNS, &p.MIMEType,
			&width, &height, &p.TakenAt, &p.ThumbnailKey, &lastSeen,
		); err != nil {
			return nil, err
		}
		if width.Valid {
			p.Width = int(width.Int64)
		}
		if height.Valid {
			p.Height = int(height.Int64)
		}
		if lastSeen.Valid {
			p.LastSeenScanID = lastSeen.Int64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteUnseen removes photos that were not marked seen by scanID and returns
// the list of thumbnail keys that were dropped (nullable rows contribute no key).
func (s *Store) DeleteUnseen(ctx context.Context, scanID int64) ([]string, int64, error) {
	rows, err := s.conn.QueryContext(ctx,
		`SELECT thumbnail_key FROM photos
		 WHERE (last_seen_scan_id IS NULL OR last_seen_scan_id != ?)
		   AND thumbnail_key IS NOT NULL`,
		scanID,
	)
	if err != nil {
		return nil, 0, err
	}
	var keys []string
	for rows.Next() {
		var k sql.NullString
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if k.Valid {
			keys = append(keys, k.String)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	res, err := s.conn.ExecContext(ctx,
		`DELETE FROM photos WHERE last_seen_scan_id IS NULL OR last_seen_scan_id != ?`,
		scanID,
	)
	if err != nil {
		return nil, 0, err
	}
	removed, _ := res.RowsAffected()
	return keys, removed, nil
}

// DeleteEmptyAlbums removes albums with no photos. Returns number removed.
func (s *Store) DeleteEmptyAlbums(ctx context.Context) (int64, error) {
	res, err := s.conn.ExecContext(ctx,
		`DELETE FROM albums WHERE id NOT IN (SELECT DISTINCT album_id FROM photos)`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteEmptyCategories removes categories with no albums. Returns number removed.
func (s *Store) DeleteEmptyCategories(ctx context.Context) (int64, error) {
	res, err := s.conn.ExecContext(ctx,
		`DELETE FROM categories WHERE id NOT IN (SELECT DISTINCT category_id FROM albums)`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CurrentThumbnailKeys returns every non-null thumbnail_key in photos.
func (s *Store) CurrentThumbnailKeys(ctx context.Context) ([]string, error) {
	rows, err := s.conn.QueryContext(ctx,
		`SELECT thumbnail_key FROM photos WHERE thumbnail_key IS NOT NULL`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k sql.NullString
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k.Valid {
			out = append(out, k.String)
		}
	}
	return out, rows.Err()
}

// ClearCatalogue removes all derived catalog rows. Used by the rebuild command;
// application-owned tables (Spec 2+) are not touched here.
func (s *Store) ClearCatalogue(ctx context.Context) error {
	stmts := []string{
		`DELETE FROM photos`,
		`DELETE FROM albums`,
		`DELETE FROM categories`,
		`DELETE FROM scan_runs`,
	}
	for _, q := range stmts {
		if _, err := s.conn.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("clear %q: %w", q, err)
		}
	}
	return nil
}

func nullableInt(n int) sql.NullInt64 {
	if n <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(n), Valid: true}
}
