package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"photo-browser/internal/database"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func fixedNow() time.Time {
	return time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
}

func TestStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := fixedNow()

	scanID, err := store.StartScan(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	catID, err := store.UpsertCategory(ctx, "旅遊", "旅遊", scanID, now)
	if err != nil {
		t.Fatal(err)
	}
	albumID, err := store.UpsertAlbum(ctx, catID, "日本", "旅遊/日本", scanID, now)
	if err != nil {
		t.Fatal(err)
	}
	photoID, err := store.InsertPhoto(ctx, Photo{
		AlbumID:      albumID,
		Filename:     "a.jpg",
		RelativePath: "旅遊/日本/a.jpg",
		MIMEType:     "image/jpeg",
		FileSize:     100,
		FileMTimeNS:  time.Now().UnixNano(),
		Width:        800,
		Height:       600,
		TakenAt:      sql.NullString{String: "2017-06-27T14:03:02", Valid: true},
		ThumbnailKey: sql.NullString{String: "1-2-3", Valid: true},
	}, scanID, now)
	if err != nil {
		t.Fatal(err)
	}
	if photoID <= 0 {
		t.Fatalf("bad photoID %d", photoID)
	}

	got, ok, err := store.FindPhoto(ctx, "旅遊/日本/a.jpg")
	if err != nil || !ok {
		t.Fatalf("FindPhoto: ok=%v err=%v", ok, err)
	}
	if got.Width != 800 || got.Height != 600 {
		t.Fatalf("dims %d,%d", got.Width, got.Height)
	}
	if got.TakenAt.String != "2017-06-27T14:03:02" {
		t.Fatalf("taken_at %q", got.TakenAt.String)
	}

	if err := store.FinishScan(ctx, scanID, "succeeded", ScanCounts{Seen: 1, New: 1}, "", now); err != nil {
		t.Fatal(err)
	}
}

func TestFindPhotoAbsent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	_, ok, err := store.FindPhoto(ctx, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false")
	}
}

func TestCascadeDeleteCategory(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := fixedNow()

	scanID, _ := store.StartScan(ctx, now)
	catID, _ := store.UpsertCategory(ctx, "旅遊", "旅遊", scanID, now)
	albumID, _ := store.UpsertAlbum(ctx, catID, "日本", "旅遊/日本", scanID, now)
	_, err := store.InsertPhoto(ctx, Photo{
		AlbumID: albumID, Filename: "a.jpg", RelativePath: "旅遊/日本/a.jpg",
		MIMEType: "image/jpeg", FileSize: 1, FileMTimeNS: 1, Width: 1, Height: 1,
	}, scanID, now)
	if err != nil {
		t.Fatal(err)
	}

	db := store.conn.(*sql.DB)
	if _, err := db.Exec(`DELETE FROM categories WHERE id=?`, catID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM photos`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("photos remaining after cascade: %d err=%v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM albums`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("albums remaining after cascade: %d err=%v", count, err)
	}
}

func TestStartScanAbandonsRunning(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := fixedNow()

	firstID, err := store.StartScan(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := store.StartScan(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if secondID == firstID {
		t.Fatalf("expected new scan id")
	}
	db := store.conn.(*sql.DB)
	var status, summary sql.NullString
	if err := db.QueryRow(`SELECT status, error_summary FROM scan_runs WHERE id=?`, firstID).Scan(&status, &summary); err != nil {
		t.Fatal(err)
	}
	if status.String != "failed" || summary.String != "abandoned by next run" {
		t.Fatalf("prior scan not abandoned: status=%q summary=%q", status.String, summary.String)
	}
	if err := db.QueryRow(`SELECT status FROM scan_runs WHERE id=?`, secondID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status.String != "running" {
		t.Fatalf("new scan status=%q", status.String)
	}
}

func TestInTxCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := fixedNow()
	scanID, _ := store.StartScan(ctx, now)

	err := store.InTx(ctx, func(s *Store) error {
		_, err := s.UpsertCategory(ctx, "旅遊", "旅遊", scanID, now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	db := store.conn.(*sql.DB)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("post-commit categories=%d err=%v", count, err)
	}

	err = store.InTx(ctx, func(s *Store) error {
		if _, err := s.UpsertCategory(ctx, "工作", "工作", scanID, now); err != nil {
			return err
		}
		return sql.ErrTxDone // arbitrary sentinel to trigger rollback
	})
	if err == nil {
		t.Fatal("expected rollback error")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback left rows: %d err=%v", count, err)
	}
}
