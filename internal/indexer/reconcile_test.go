package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"photo-browser/internal/media"
	"photo-browser/internal/scanner"
)

// setupTwoPhotos indexes two photos in album 旅遊/日本 and returns:
//   - the indexer wired to a fresh store
//   - the created thumbnail file paths (both should exist on disk after)
func setupTwoPhotos(t *testing.T) (*Indexer, string, [2]string) {
	t.Helper()
	entries := []scanner.Entry{
		entry("旅遊/日本/a.jpg", 100, 1_000_000),
		entry("旅遊/日本/b.jpg", 200, 2_000_000),
	}
	idx, _, _ := newIndexer(t, entries, nil)
	thumbsDir := t.TempDir()
	idx.ThumbnailDir = thumbsDir
	// Real thumbnail fake: write a file per key so we can observe removal.
	idx.Thumbnail = func(ctx context.Context, source, key string, srcMaxDim int) error {
		return os.WriteFile(filepath.Join(thumbsDir, key+".webp"), []byte("t"), 0o644)
	}
	if _, err := idx.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	rows, err := idx.Store.ListUnseenPhotos(context.Background(), 999_999) // arbitrary future scan id
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 photos after seed, got %d", len(rows))
	}
	var paths [2]string
	for i, r := range rows {
		paths[i] = filepath.Join(thumbsDir, r.ThumbnailKey.String+".webp")
		if _, err := os.Stat(paths[i]); err != nil {
			t.Fatalf("thumbnail missing: %v", err)
		}
	}
	return idx, thumbsDir, paths
}

func TestReconcileDeletesUnseenAndOrphansOnly(t *testing.T) {
	ctx := context.Background()
	idx, thumbsDir, _ := setupTwoPhotos(t)

	// Second scan keeps only a.jpg — b.jpg disappears from the filesystem.
	idx.Walk = func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
		return []scanner.Entry{entry("旅遊/日本/a.jpg", 100, 1_000_000)}, nil
	}
	counts, err := idx.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.Removed != 1 {
		t.Fatalf("Removed=%d want 1", counts.Removed)
	}

	// Photo b.jpg is gone from DB; a.jpg remains.
	_, ok, _ := idx.Store.FindPhoto(ctx, "旅遊/日本/b.jpg")
	if ok {
		t.Fatal("b.jpg should be deleted")
	}
	_, ok, _ = idx.Store.FindPhoto(ctx, "旅遊/日本/a.jpg")
	if !ok {
		t.Fatal("a.jpg should remain")
	}

	// b.jpg's thumbnail file is deleted; a.jpg's remains.
	remaining, err := os.ReadDir(thumbsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 thumbnail file, got %d: %v", len(remaining), remaining)
	}
}

func TestReconcileDeletesEmptyAlbumAndCategory(t *testing.T) {
	ctx := context.Background()
	idx, _, _ := setupTwoPhotos(t)

	idx.Walk = func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
		return nil, nil
	}
	if _, err := idx.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}

	db := idx.Store.RawDB()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM albums`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("albums=%d err=%v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("categories=%d err=%v", count, err)
	}
}

func TestWalkErrorPreservesUnseenRowsAndThumbnails(t *testing.T) {
	ctx := context.Background()
	idx, thumbsDir, paths := setupTwoPhotos(t)

	sentinel := errors.New("boom")
	idx.Walk = func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
		return nil, sentinel
	}
	_, err := idx.Run(ctx, Options{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}

	// Both photos still present.
	_, ok1, _ := idx.Store.FindPhoto(ctx, "旅遊/日本/a.jpg")
	_, ok2, _ := idx.Store.FindPhoto(ctx, "旅遊/日本/b.jpg")
	if !ok1 || !ok2 {
		t.Fatalf("photos removed on walk failure: a=%v b=%v", ok1, ok2)
	}
	// Both thumbnail files still exist.
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("thumbnail missing after failed walk: %v", err)
		}
	}
	// Ensure orphan cleanup did NOT run either — every file we placed is still there.
	remaining, _ := os.ReadDir(thumbsDir)
	if len(remaining) != 2 {
		t.Fatalf("expected 2 thumbnail files, got %d", len(remaining))
	}
}

func TestReconcileMissingThumbnailFileIsNotAWarning(t *testing.T) {
	ctx := context.Background()
	idx, thumbsDir, paths := setupTwoPhotos(t)

	// Manually delete b.jpg's thumbnail file before reconciliation runs.
	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}
	idx.Walk = func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
		return []scanner.Entry{entry("旅遊/日本/a.jpg", 100, 1_000_000)}, nil
	}
	counts, err := idx.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.Warnings != 0 {
		t.Fatalf("Warnings=%d want 0 (ErrNotExist is success)", counts.Warnings)
	}
	remaining, _ := os.ReadDir(thumbsDir)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
}

func TestReconcileDeletesOrphanFilesOnDisk(t *testing.T) {
	ctx := context.Background()
	idx, thumbsDir, _ := setupTwoPhotos(t)

	// Plant an extra .webp not associated with any photo.
	orphan := filepath.Join(thumbsDir, "orphan-999.webp")
	if err := os.WriteFile(orphan, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also a non-webp file that must not be touched.
	keepMe := filepath.Join(thumbsDir, "notes.txt")
	if err := os.WriteFile(keepMe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Trigger a successful reconciliation (no photos change but the walk finishes).
	idx.Walk = func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
		return []scanner.Entry{
			entry("旅遊/日本/a.jpg", 100, 1_000_000),
			entry("旅遊/日本/b.jpg", 200, 2_000_000),
		}, nil
	}
	if _, err := idx.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("orphan .webp should be deleted")
	}
	if _, err := os.Stat(keepMe); err != nil {
		t.Fatalf("non-webp must be preserved: %v", err)
	}
}

func TestRemoveThumbnailRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", ".", "..", "a/b", "/abs", "../evil"} {
		if err := RemoveThumbnail(dir, bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

// Sanity: the seeded fake metadata must succeed so downstream reconciliation
// logic gets meaningful photo state.
func TestSetupHelperSanity(t *testing.T) {
	_, _, paths := setupTwoPhotos(t)
	if paths[0] == paths[1] {
		t.Fatal("thumbnail paths collided")
	}
	_ = media.Metadata{} // silence unused import if this shrinks
}
