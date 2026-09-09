package catalog_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"photo-browser/internal/catalog"
	"photo-browser/internal/database"
)

type seed struct {
	store *catalog.Store
	ctx   context.Context
}

func newSeed(t *testing.T) *seed {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &seed{store: catalog.NewStore(db), ctx: context.Background()}
}

func (s *seed) mustScan(t *testing.T, now time.Time) int64 {
	t.Helper()
	id, err := s.store.StartScan(s.ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestListCategoriesOrderedByName(t *testing.T) {
	s := newSeed(t)
	scanID := s.mustScan(t, time.Unix(1_700_000_000, 0))
	for _, name := range []string{"zeta", "alpha", "mu"} {
		if _, err := s.store.UpsertCategory(s.ctx, name, name, scanID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.store.ListCategories(s.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "alpha" || got[2].Name != "zeta" {
		t.Fatalf("categories=%v", got)
	}
}

func TestGetCategoryMissing(t *testing.T) {
	s := newSeed(t)
	_, ok, err := s.store.GetCategory(s.ctx, 42)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestListAlbumsByCategory(t *testing.T) {
	s := newSeed(t)
	scanID := s.mustScan(t, time.Now())
	catID, _ := s.store.UpsertCategory(s.ctx, "trav", "trav", scanID, time.Now())
	otherCat, _ := s.store.UpsertCategory(s.ctx, "misc", "misc", scanID, time.Now())
	for _, name := range []string{"beta", "alpha"} {
		if _, err := s.store.UpsertAlbum(s.ctx, catID, name, "trav/"+name, scanID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = s.store.UpsertAlbum(s.ctx, otherCat, "other", "misc/other", scanID, time.Now())
	got, err := s.store.ListAlbumsByCategory(s.ctx, catID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("albums=%v", got)
	}
}

func TestListAlbumPhotosNullsLast(t *testing.T) {
	s := newSeed(t)
	scanID := s.mustScan(t, time.Now())
	catID, _ := s.store.UpsertCategory(s.ctx, "trav", "trav", scanID, time.Now())
	albumID, _ := s.store.UpsertAlbum(s.ctx, catID, "japan", "trav/japan", scanID, time.Now())
	insert := func(name, takenAt string) int64 {
		p := catalog.Photo{
			AlbumID: albumID, Filename: name, RelativePath: "trav/japan/" + name,
			MIMEType: "image/jpeg", FileSize: 100, FileMTimeNS: 200,
		}
		if takenAt != "" {
			p.TakenAt = sql.NullString{String: takenAt, Valid: true}
		}
		id, err := s.store.InsertPhoto(s.ctx, p, scanID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	insert("a.jpg", "2024-01-01T00:00:00")
	insert("b.jpg", "2025-01-01T00:00:00")
	insert("c.jpg", "")
	insert("d.jpg", "")

	page1, cur, err := s.store.ListAlbumPhotos(s.ctx, albumID,
		catalog.PageCursor{IsFirst: true}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[0].Filename != "b.jpg" || page1[1].Filename != "a.jpg" {
		t.Fatalf("page1=%v", filenames(page1))
	}
	if cur.Secondary == "" {
		t.Fatal("cursor secondary should be set after non-null page")
	}

	// Second page transitions into NULL section.
	page2, cur2, err := s.store.ListAlbumPhotos(s.ctx, albumID, cur, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2=%v", filenames(page2))
	}
	if !(page2[0].Filename == "d.jpg" || page2[0].Filename == "c.jpg") {
		t.Fatalf("expected null-taken_at rows, got=%v", filenames(page2))
	}
	if cur2.Secondary != "" {
		t.Fatalf("null-section cursor should have empty secondary, got %q", cur2.Secondary)
	}

	// After exhausting the album, cursor returns zero.
	_, cur3, err := s.store.ListAlbumPhotos(s.ctx, albumID, cur2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if cur3 != (catalog.PageCursor{}) {
		t.Fatalf("expected zero cursor at end, got %+v", cur3)
	}
}

func TestListTimelineExcludesNull(t *testing.T) {
	s := newSeed(t)
	scanID := s.mustScan(t, time.Now())
	catID, _ := s.store.UpsertCategory(s.ctx, "t", "t", scanID, time.Now())
	albumID, _ := s.store.UpsertAlbum(s.ctx, catID, "a", "t/a", scanID, time.Now())
	for i := 0; i < 3; i++ {
		p := catalog.Photo{
			AlbumID:  albumID,
			Filename: fmt.Sprintf("p%d.jpg", i),
			RelativePath: fmt.Sprintf("t/a/p%d.jpg", i),
			MIMEType:     "image/jpeg", FileSize: 100, FileMTimeNS: int64(i),
			TakenAt: sql.NullString{String: fmt.Sprintf("2024-01-0%dT00:00:00", i+1), Valid: true},
		}
		if _, err := s.store.InsertPhoto(s.ctx, p, scanID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	// one null-taken_at row must be excluded from timeline
	_, err := s.store.InsertPhoto(s.ctx, catalog.Photo{
		AlbumID: albumID, Filename: "n.jpg", RelativePath: "t/a/n.jpg",
		MIMEType: "image/jpeg", FileSize: 10, FileMTimeNS: 99,
	}, scanID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tl, _, err := s.store.ListTimeline(s.ctx, catalog.PageCursor{IsFirst: true}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl) != 3 {
		t.Fatalf("timeline len=%d", len(tl))
	}
	for _, p := range tl {
		if !p.TakenAt.Valid {
			t.Fatalf("null taken_at leaked: %+v", p)
		}
	}
}

func TestAlbumCoverThumbnail(t *testing.T) {
	s := newSeed(t)
	scanID := s.mustScan(t, time.Now())
	catID, _ := s.store.UpsertCategory(s.ctx, "c", "c", scanID, time.Now())
	albumID, _ := s.store.UpsertAlbum(s.ctx, catID, "a", "c/a", scanID, time.Now())
	// first has no thumbnail
	_, _ = s.store.InsertPhoto(s.ctx, catalog.Photo{
		AlbumID: albumID, Filename: "a.jpg", RelativePath: "c/a/a.jpg",
		MIMEType: "image/jpeg", FileSize: 1, FileMTimeNS: 1,
		TakenAt: sql.NullString{String: "2025-01-01T00:00:00", Valid: true},
	}, scanID, time.Now())
	id, _ := s.store.InsertPhoto(s.ctx, catalog.Photo{
		AlbumID: albumID, Filename: "b.jpg", RelativePath: "c/a/b.jpg",
		MIMEType: "image/jpeg", FileSize: 1, FileMTimeNS: 1,
		TakenAt:      sql.NullString{String: "2024-01-01T00:00:00", Valid: true},
		ThumbnailKey: sql.NullString{String: "key-b", Valid: true},
	}, scanID, time.Now())
	_ = id
	got, err := s.store.AlbumCoverThumbnail(s.ctx, albumID)
	if err != nil {
		t.Fatal(err)
	}
	if got.String != "key-b" {
		t.Fatalf("cover=%q want key-b", got.String)
	}
}

func TestListAlbumsPaginated(t *testing.T) {
	s := newSeed(t)
	scanID := s.mustScan(t, time.Now())
	catID, _ := s.store.UpsertCategory(s.ctx, "c", "c", scanID, time.Now())
	base := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if _, err := s.store.UpsertAlbum(s.ctx, catID, fmt.Sprintf("a%d", i), fmt.Sprintf("c/a%d", i),
			scanID, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]bool{}
	cur := catalog.PageCursor{IsFirst: true}
	for i := 0; i < 5; i++ {
		page, next, err := s.store.ListAlbums(s.ctx, cur, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page {
			if seen[a.ID] {
				t.Fatalf("duplicate album id=%d", a.ID)
			}
			seen[a.ID] = true
		}
		if next == (catalog.PageCursor{}) {
			break
		}
		cur = next
	}
	if len(seen) != 5 {
		t.Fatalf("total seen=%d want 5", len(seen))
	}
}

func filenames(ps []catalog.Photo) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Filename
	}
	return out
}
