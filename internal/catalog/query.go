package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PageCursor is the storage-layer view of the opaque cursor exposed by
// httpapi.Cursor. Secondary is either a taken_at / updated_at ISO string or
// empty; ID is the tiebreaker.
//
// First page is signaled by IsFirst=true.
type PageCursor struct {
	IsFirst   bool
	Secondary string
	ID        int64
}

// ScanRun is a read-only projection of scan_runs; used by admin API.
type ScanRun struct {
	ID           int64
	StartedAt    string
	FinishedAt   sql.NullString
	Status       string
	Counts       ScanCounts
	ErrorSummary sql.NullString
}

// ListCategories returns categories ordered by name.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.conn.QueryContext(ctx,
		`SELECT id, name, relative_path, COALESCE(last_seen_scan_id, 0)
		 FROM categories ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.RelativePath, &c.LastSeenScanID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCategory fetches a single category by id.
func (s *Store) GetCategory(ctx context.Context, id int64) (Category, bool, error) {
	var c Category
	err := s.conn.QueryRowContext(ctx,
		`SELECT id, name, relative_path, COALESCE(last_seen_scan_id, 0)
		 FROM categories WHERE id=?`, id,
	).Scan(&c.ID, &c.Name, &c.RelativePath, &c.LastSeenScanID)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, false, nil
	}
	if err != nil {
		return Category{}, false, err
	}
	return c, true, nil
}

// ListAlbumsByCategory returns albums under a category, ordered by name.
func (s *Store) ListAlbumsByCategory(ctx context.Context, categoryID int64) ([]Album, error) {
	rows, err := s.conn.QueryContext(ctx,
		`SELECT id, category_id, name, relative_path, COALESCE(last_seen_scan_id, 0)
		 FROM albums WHERE category_id=? ORDER BY name ASC`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlbumRows(rows)
}

// ListAlbums paginates albums ordered by updated_at DESC, id DESC.
// Returns next cursor set when a further page is available.
func (s *Store) ListAlbums(ctx context.Context, cur PageCursor, limit int) ([]Album, PageCursor, error) {
	q := `SELECT id, category_id, name, relative_path, COALESCE(last_seen_scan_id, 0), updated_at
	      FROM albums`
	args := []any{}
	if !cur.IsFirst {
		q += ` WHERE (updated_at < ? OR (updated_at = ? AND id < ?))`
		args = append(args, cur.Secondary, cur.Secondary, cur.ID)
	}
	q += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, PageCursor{}, err
	}
	defer rows.Close()

	var albums []Album
	var lastUpdatedAt string
	for rows.Next() {
		var a Album
		var updatedAt string
		if err := rows.Scan(&a.ID, &a.CategoryID, &a.Name, &a.RelativePath, &a.LastSeenScanID, &updatedAt); err != nil {
			return nil, PageCursor{}, err
		}
		albums = append(albums, a)
		lastUpdatedAt = updatedAt
	}
	if err := rows.Err(); err != nil {
		return nil, PageCursor{}, err
	}
	if len(albums) > limit {
		albums = albums[:limit]
		last := albums[limit-1]
		return albums, PageCursor{Secondary: lastUpdatedAt, ID: last.ID}, nil
	}
	return albums, PageCursor{}, nil
}

// GetAlbum fetches a single album by id.
func (s *Store) GetAlbum(ctx context.Context, id int64) (Album, bool, error) {
	var a Album
	err := s.conn.QueryRowContext(ctx,
		`SELECT id, category_id, name, relative_path, COALESCE(last_seen_scan_id, 0)
		 FROM albums WHERE id=?`, id,
	).Scan(&a.ID, &a.CategoryID, &a.Name, &a.RelativePath, &a.LastSeenScanID)
	if errors.Is(err, sql.ErrNoRows) {
		return Album{}, false, nil
	}
	if err != nil {
		return Album{}, false, err
	}
	return a, true, nil
}

// ListAlbumPhotos paginates photos in an album with NULLS-LAST ordering.
func (s *Store) ListAlbumPhotos(ctx context.Context, albumID int64, cur PageCursor, limit int) ([]Photo, PageCursor, error) {
	return s.listPhotosPaged(ctx, `album_id=?`, []any{albumID}, cur, limit)
}

// ListPhotos paginates every photo in the catalogue with NULLS-LAST ordering.
func (s *Store) ListPhotos(ctx context.Context, cur PageCursor, limit int) ([]Photo, PageCursor, error) {
	return s.listPhotosPaged(ctx, ``, nil, cur, limit)
}

// ListTimeline paginates photos whose taken_at is not null, DESC by taken_at.
func (s *Store) ListTimeline(ctx context.Context, cur PageCursor, limit int) ([]Photo, PageCursor, error) {
	where := `taken_at IS NOT NULL`
	args := []any{}
	if !cur.IsFirst && cur.Secondary != "" {
		where += ` AND (taken_at < ? OR (taken_at = ? AND id < ?))`
		args = append(args, cur.Secondary, cur.Secondary, cur.ID)
	}
	q := fmt.Sprintf(`SELECT %s FROM photos WHERE %s ORDER BY taken_at DESC, id DESC LIMIT ?`,
		photoColumns, where)
	args = append(args, limit+1)
	rows, err := s.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, PageCursor{}, err
	}
	defer rows.Close()
	photos, err := scanPhotoRows(rows)
	if err != nil {
		return nil, PageCursor{}, err
	}
	if len(photos) > limit {
		photos = photos[:limit]
		last := photos[limit-1]
		return photos, PageCursor{Secondary: last.TakenAt.String, ID: last.ID}, nil
	}
	return photos, PageCursor{}, nil
}

// GetPhoto fetches a single photo by id.
func (s *Store) GetPhoto(ctx context.Context, id int64) (Photo, bool, error) {
	row := s.conn.QueryRowContext(ctx,
		`SELECT `+photoColumns+` FROM photos WHERE id=?`, id)
	p, err := scanPhotoRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Photo{}, false, nil
	}
	if err != nil {
		return Photo{}, false, err
	}
	return p, true, nil
}

// AlbumCoverThumbnail returns the first non-null thumbnail_key in the album's
// photo ordering (taken_at DESC NULLS LAST, id DESC).
func (s *Store) AlbumCoverThumbnail(ctx context.Context, albumID int64) (sql.NullString, error) {
	var k sql.NullString
	err := s.conn.QueryRowContext(ctx,
		`SELECT thumbnail_key
		 FROM photos
		 WHERE album_id=? AND thumbnail_key IS NOT NULL
		 ORDER BY (taken_at IS NULL), taken_at DESC, id DESC
		 LIMIT 1`, albumID,
	).Scan(&k)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, nil
	}
	return k, err
}

// GetScanRun fetches a scan_runs row projected as ScanRun.
func (s *Store) GetScanRun(ctx context.Context, id int64) (ScanRun, bool, error) {
	var r ScanRun
	err := s.conn.QueryRowContext(ctx,
		`SELECT id, started_at, finished_at, status,
		        files_seen, files_new, files_changed, files_unchanged, files_removed,
		        warnings_count, errors_count, error_summary
		 FROM scan_runs WHERE id=?`, id,
	).Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.Status,
		&r.Counts.Seen, &r.Counts.New, &r.Counts.Changed, &r.Counts.Unchanged, &r.Counts.Removed,
		&r.Counts.Warnings, &r.Counts.Errors, &r.ErrorSummary)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanRun{}, false, nil
	}
	if err != nil {
		return ScanRun{}, false, err
	}
	return r, true, nil
}

// listPhotosPaged is the shared paging query for ListPhotos / ListAlbumPhotos.
// The order is taken_at DESC NULLS LAST, id DESC. Cursor semantics:
//
//	IsFirst=true                       -> start
//	Secondary != ""                    -> in non-null section, next: rows with (taken_at,id) < (Secondary,ID)
//	                                      or any null row.
//	Secondary == "" && ID != 0         -> in null section, next: rows where taken_at IS NULL AND id < ID.
func (s *Store) listPhotosPaged(
	ctx context.Context,
	baseWhere string,
	baseArgs []any,
	cur PageCursor,
	limit int,
) ([]Photo, PageCursor, error) {
	where := baseWhere
	args := append([]any{}, baseArgs...)

	switch {
	case cur.IsFirst:
		// no cursor clause
	case cur.Secondary != "":
		clause := `((taken_at IS NOT NULL AND (taken_at < ? OR (taken_at = ? AND id < ?))) OR taken_at IS NULL)`
		if where != "" {
			where = "(" + where + ") AND " + clause
		} else {
			where = clause
		}
		args = append(args, cur.Secondary, cur.Secondary, cur.ID)
	default:
		clause := `(taken_at IS NULL AND id < ?)`
		if where != "" {
			where = "(" + where + ") AND " + clause
		} else {
			where = clause
		}
		args = append(args, cur.ID)
	}

	q := `SELECT ` + photoColumns + ` FROM photos`
	if where != "" {
		q += ` WHERE ` + where
	}
	q += ` ORDER BY (taken_at IS NULL), taken_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, PageCursor{}, err
	}
	defer rows.Close()
	photos, err := scanPhotoRows(rows)
	if err != nil {
		return nil, PageCursor{}, err
	}
	if len(photos) > limit {
		photos = photos[:limit]
		last := photos[limit-1]
		return photos, PageCursor{Secondary: last.TakenAt.String, ID: last.ID}, nil
	}
	return photos, PageCursor{}, nil
}

const photoColumns = `id, album_id, filename, relative_path, file_size, file_mtime_ns, mime_type,
	width, height, taken_at, thumbnail_key, COALESCE(last_seen_scan_id, 0)`

func scanAlbumRows(rows *sql.Rows) ([]Album, error) {
	var out []Album
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.CategoryID, &a.Name, &a.RelativePath, &a.LastSeenScanID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanPhotoRows(rows *sql.Rows) ([]Photo, error) {
	var out []Photo
	for rows.Next() {
		p, err := scanPhotoRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type photoScanner interface {
	Scan(dest ...any) error
}

func scanPhotoRow(r photoScanner) (Photo, error) {
	var p Photo
	var width, height sql.NullInt64
	if err := r.Scan(
		&p.ID, &p.AlbumID, &p.Filename, &p.RelativePath, &p.FileSize, &p.FileMTimeNS, &p.MIMEType,
		&width, &height, &p.TakenAt, &p.ThumbnailKey, &p.LastSeenScanID,
	); err != nil {
		return Photo{}, err
	}
	if width.Valid {
		p.Width = int(width.Int64)
	}
	if height.Valid {
		p.Height = int(height.Int64)
	}
	return p, nil
}
