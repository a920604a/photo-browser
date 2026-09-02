package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"photo-browser/internal/catalog"
	"photo-browser/internal/database"
	"photo-browser/internal/media"
	"photo-browser/internal/scanner"
)

func newStore(t *testing.T) *catalog.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return catalog.NewStore(db)
}

func fixedNow() time.Time { return time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC) }

func entry(rel string, size, mtime int64) scanner.Entry {
	return scanner.Entry{
		CategoryName: "旅遊", CategoryPath: "旅遊",
		AlbumName: "日本", AlbumPath: "旅遊/日本",
		Filename:     filepath.Base(rel),
		RelativePath: rel,
		Size:         size,
		MTimeNS:      mtime,
	}
}

type spies struct {
	metaCalls  int32
	thumbCalls int32
}

func newIndexer(t *testing.T, entries []scanner.Entry, walkErr error) (*Indexer, *catalog.Store, *spies) {
	t.Helper()
	store := newStore(t)
	sp := &spies{}
	idx := &Indexer{
		Store:     store,
		PhotoRoot: t.TempDir(),
		Walk: func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
			return entries, walkErr
		},
		ReadMetadata: func(path string) (media.Metadata, error) {
			atomic.AddInt32(&sp.metaCalls, 1)
			return media.Metadata{MIMEType: "image/jpeg", Width: 100, Height: 100}, nil
		},
		Thumbnail: func(ctx context.Context, source, key string, srcMaxDim int) error {
			atomic.AddInt32(&sp.thumbCalls, 1)
			return nil
		},
		BatchSize: DefaultBatchSize,
		Now:       fixedNow,
	}
	return idx, store, sp
}

func TestRunNewPhotoInsertsAndThumbnails(t *testing.T) {
	entries := []scanner.Entry{entry("旅遊/日本/a.jpg", 100, 1_000_000)}
	idx, store, sp := newIndexer(t, entries, nil)
	counts, err := idx.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.New != 1 || counts.Changed != 0 || counts.Unchanged != 0 {
		t.Fatalf("counts=%+v", counts)
	}
	if sp.metaCalls != 1 {
		t.Fatalf("meta calls=%d", sp.metaCalls)
	}
	if sp.thumbCalls != 1 {
		t.Fatalf("thumb calls=%d", sp.thumbCalls)
	}
	p, ok, err := store.FindPhoto(context.Background(), "旅遊/日本/a.jpg")
	if err != nil || !ok {
		t.Fatalf("photo not stored: ok=%v err=%v", ok, err)
	}
	if !p.ThumbnailKey.Valid {
		t.Fatal("expected thumbnail_key populated")
	}
	if p.ThumbnailKey.String != ThumbnailKey(p.ID, 1_000_000, 100) {
		t.Fatalf("bad thumbnail key %q", p.ThumbnailKey.String)
	}
}

func TestRunChangedRewritesFingerprintAndThumbnail(t *testing.T) {
	ctx := context.Background()
	// First scan
	entries := []scanner.Entry{entry("旅遊/日本/a.jpg", 100, 1_000_000)}
	idx, store, sp := newIndexer(t, entries, nil)
	if _, err := idx.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}
	first, _, _ := store.FindPhoto(ctx, "旅遊/日本/a.jpg")

	// Second scan with different size
	entries[0].Size = 250
	entries[0].MTimeNS = 2_000_000
	counts, err := idx.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.New != 0 || counts.Changed != 1 {
		t.Fatalf("counts=%+v", counts)
	}
	if sp.metaCalls != 2 || sp.thumbCalls != 2 {
		t.Fatalf("metaCalls=%d thumbCalls=%d", sp.metaCalls, sp.thumbCalls)
	}
	after, _, _ := store.FindPhoto(ctx, "旅遊/日本/a.jpg")
	if after.ID != first.ID {
		t.Fatalf("photo id changed: was %d now %d", first.ID, after.ID)
	}
	if after.FileSize != 250 || after.FileMTimeNS != 2_000_000 {
		t.Fatalf("fingerprint not updated: %+v", after)
	}
	if after.ThumbnailKey.String == first.ThumbnailKey.String {
		t.Fatalf("thumbnail key unchanged: %q", after.ThumbnailKey.String)
	}
}

func TestRunUnchangedSkipsMetadataAndThumbnail(t *testing.T) {
	ctx := context.Background()
	entries := []scanner.Entry{entry("旅遊/日本/a.jpg", 100, 1_000_000)}
	idx, store, sp := newIndexer(t, entries, nil)
	if _, err := idx.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}
	baselineMeta := sp.metaCalls
	baselineThumb := sp.thumbCalls

	counts, err := idx.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.Unchanged != 1 || counts.New != 0 || counts.Changed != 0 {
		t.Fatalf("counts=%+v", counts)
	}
	if sp.metaCalls != baselineMeta {
		t.Fatalf("meta called for unchanged: %d -> %d", baselineMeta, sp.metaCalls)
	}
	if sp.thumbCalls != baselineThumb {
		t.Fatalf("thumb called for unchanged: %d -> %d", baselineThumb, sp.thumbCalls)
	}

	p, _, _ := store.FindPhoto(ctx, "旅遊/日本/a.jpg")
	// last_seen_scan_id must advance
	var lastSeen int64
	if err := getDB(store).QueryRow(`SELECT last_seen_scan_id FROM photos WHERE id=?`, p.ID).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if lastSeen != 2 {
		t.Fatalf("last_seen_scan_id=%d want 2", lastSeen)
	}
}

func TestRunRebuildThumbnailsUnchangedIdentity(t *testing.T) {
	ctx := context.Background()
	entries := []scanner.Entry{entry("旅遊/日本/a.jpg", 100, 1_000_000)}
	idx, store, sp := newIndexer(t, entries, nil)
	if _, err := idx.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := store.FindPhoto(ctx, "旅遊/日本/a.jpg")
	baselineMeta := sp.metaCalls

	counts, err := idx.Run(ctx, Options{RebuildThumbnails: true})
	if err != nil {
		t.Fatal(err)
	}
	if counts.Unchanged != 1 {
		t.Fatalf("counts=%+v", counts)
	}
	if sp.metaCalls != baselineMeta {
		t.Fatalf("metadata re-read on rebuild-thumbnails: %d -> %d", baselineMeta, sp.metaCalls)
	}
	if sp.thumbCalls != 2 {
		t.Fatalf("thumbnail regenerated count = %d want 2", sp.thumbCalls)
	}
	after, _, _ := store.FindPhoto(ctx, "旅遊/日本/a.jpg")
	if after.ID != before.ID {
		t.Fatalf("identity changed: %d -> %d", before.ID, after.ID)
	}
	if after.FileSize != before.FileSize || after.FileMTimeNS != before.FileMTimeNS {
		t.Fatalf("fingerprint changed")
	}
}

func TestRunWalkErrorFailsScan(t *testing.T) {
	sentinel := errors.New("walk broke")
	idx, store, _ := newIndexer(t, nil, sentinel)
	_, err := idx.Run(context.Background(), Options{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
	// scan_runs should have one failed row with the error summary
	var status, summary sql.NullString
	if err := getDB(store).QueryRow(`SELECT status, error_summary FROM scan_runs ORDER BY id DESC LIMIT 1`).Scan(&status, &summary); err != nil {
		t.Fatal(err)
	}
	if status.String != "failed" || summary.String == "" {
		t.Fatalf("bad scan_run terminal state: status=%q summary=%q", status.String, summary.String)
	}
}

func TestRunMetadataErrorStillCreatesPhoto(t *testing.T) {
	ctx := context.Background()
	entries := []scanner.Entry{entry("旅遊/日本/a.jpg", 100, 1_000_000)}
	idx, store, _ := newIndexer(t, entries, nil)
	idx.ReadMetadata = func(path string) (media.Metadata, error) {
		return media.Metadata{}, errors.New("decode fail")
	}
	counts, err := idx.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.New != 1 {
		t.Fatalf("counts=%+v", counts)
	}
	if counts.Warnings < 1 {
		t.Fatalf("expected warning, got %d", counts.Warnings)
	}
	p, ok, err := store.FindPhoto(ctx, "旅遊/日本/a.jpg")
	if err != nil || !ok {
		t.Fatalf("photo missing")
	}
	if p.ThumbnailKey.Valid {
		t.Fatalf("expected null thumbnail_key on decode failure")
	}
	if p.MIMEType != "image/jpeg" {
		t.Fatalf("expected fallback mime, got %q", p.MIMEType)
	}
}

func TestRunBatchBoundary(t *testing.T) {
	ctx := context.Background()
	var entries []scanner.Entry
	for i := 0; i < 401; i++ {
		entries = append(entries, entry(fmt.Sprintf("旅遊/日本/img_%03d.jpg", i), int64(100+i), int64(1_000_000+i)))
	}
	idx, store, _ := newIndexer(t, entries, nil)
	idx.BatchSize = 200

	var commits int32
	store.TxHook = func() { atomic.AddInt32(&commits, 1) }

	counts, err := idx.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.New != 401 {
		t.Fatalf("counts.New=%d want 401", counts.New)
	}
	// 3 batches of 200/200/1 = 3 processing commits + 1 reconciliation commit.
	if got := atomic.LoadInt32(&commits); got != 4 {
		t.Fatalf("commits=%d want 4 (3 batch + 1 reconcile)", got)
	}
}

func getDB(s *catalog.Store) *sql.DB {
	return s.RawDB()
}
