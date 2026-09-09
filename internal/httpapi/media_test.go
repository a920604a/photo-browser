package httpapi_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"photo-browser/internal/auth"
	"photo-browser/internal/catalog"
	"photo-browser/internal/config"
	"photo-browser/internal/database"
	"photo-browser/internal/httpapi"
	"photo-browser/internal/users"
)

func seedMedia(t *testing.T) *seedResult {
	t.Helper()
	signer := auth.NewTestSigner(kid)
	jwks := httptest.NewServer(newJWKSHandler(signer))
	t.Cleanup(jwks.Close)

	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cs := catalog.NewStore(db)
	us := users.NewStore(db)
	_, _ = us.Add(context.Background(), users.User{
		FirebaseUID: sql.NullString{String: "uid-a", Valid: true},
		Role:        "member", Enabled: true,
	}, time.Now())

	scanID, _ := cs.StartScan(context.Background(), time.Now())
	catID, _ := cs.UpsertCategory(context.Background(), "travel", "travel", scanID, time.Now())
	albumID, _ := cs.UpsertAlbum(context.Background(), catID, "japan 東京", "travel/japan 東京", scanID, time.Now())
	id, err := cs.InsertPhoto(context.Background(), catalog.Photo{
		AlbumID: albumID, Filename: "a photo.jpg",
		RelativePath: "travel/japan 東京/a photo.jpg",
		MIMEType:     "image/jpeg", FileSize: 12345, FileMTimeNS: 987654321,
		TakenAt:      sql.NullString{String: "2024-01-01T00:00:00", Valid: true},
		ThumbnailKey: sql.NullString{String: "1-987654321-12345", Valid: true},
	}, scanID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = cs.FinishScan(context.Background(), scanID, "succeeded", catalog.ScanCounts{Seen: 1, New: 1}, "", time.Now())

	// second photo with NULL thumbnail_key for negative test
	_, _ = cs.InsertPhoto(context.Background(), catalog.Photo{
		AlbumID: albumID, Filename: "no-thumb.jpg",
		RelativePath: "travel/japan 東京/no-thumb.jpg",
		MIMEType:     "image/jpeg", FileSize: 10, FileMTimeNS: 20,
	}, scanID, time.Now())

	v := &auth.Verifier{Issuer: iss, Audience: aud, JWKSURL: jwks.URL,
		Now: time.Now, Refresh: time.Hour}
	router := httpapi.NewRouter(httpapi.RouterDeps{
		Config: config.Config{
			InternalOriginals:  "/internal-media/originals",
			InternalThumbnails: "/internal-media/thumbnails",
		},
		Users: us, Catalog: cs, Verifier: v, Now: time.Now,
	})
	return &seedResult{router: router, signer: signer, users: us, catalog: cs, photoID: id, albumID: albumID}
}

func newJWKSHandler(s *auth.TestSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(s.JWKS())
	}
}

func TestThumbnailRedirect(t *testing.T) {
	f := seedMedia(t)
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/photos/%d/thumbnail/1-987654321-12345", f.photoID))
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("X-Accel-Redirect"); got != "/internal-media/thumbnails/1-987654321-12345.webp" {
		t.Fatalf("redirect=%q", got)
	}
	if rec.Header().Get("Cache-Control") != "private, max-age=86400, immutable" {
		t.Fatalf("cache=%q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("ETag") != `"1-987654321-12345"` {
		t.Fatalf("etag=%q", rec.Header().Get("ETag"))
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body must be empty, got %q", rec.Body)
	}
}

func TestThumbnailKeyMismatch(t *testing.T) {
	f := seedMedia(t)
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/photos/%d/thumbnail/wrong-key", f.photoID))
	if rec.Code != 404 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestThumbnailPathTraversalBlocked(t *testing.T) {
	f := seedMedia(t)
	// server-side URL routing won't match a segment containing an unescaped slash,
	// but a client can send percent-encoded slashes. Verify handler rejects.
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/photos/%d/thumbnail/%s", f.photoID, "..%2f..%2fetc%2fpasswd"))
	if rec.Code != 400 && rec.Code != 404 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("X-Accel-Redirect"); got != "" {
		t.Fatalf("unexpected redirect=%q", got)
	}
}

func TestOriginalRedirectEscapesPath(t *testing.T) {
	f := seedMedia(t)
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/photos/%d/original", f.photoID))
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	got := rec.Header().Get("X-Accel-Redirect")
	// space escaped, unicode preserved
	want := "/internal-media/originals/travel/japan%20東京/a%20photo.jpg"
	if got != want {
		t.Fatalf("redirect=%q want %q", got, want)
	}
	if rec.Header().Get("ETag") != `"12345-987654321"` {
		t.Fatalf("etag=%q", rec.Header().Get("ETag"))
	}
	if rec.Header().Get("Cache-Control") != "private, max-age=3600" {
		t.Fatalf("cache=%q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("content-type=%q", rec.Header().Get("Content-Type"))
	}
}

func TestOriginalMissingPhoto(t *testing.T) {
	f := seedMedia(t)
	rec := doGET(t, f.router, memberToken(f), "/api/v1/photos/999999/original")
	if rec.Code != 404 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestThumbnailNilKey(t *testing.T) {
	f := seedMedia(t)
	// second photo has NULL thumbnail_key; any request must 404
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/photos/%d/thumbnail/anything", f.photoID+1))
	if rec.Code != 404 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}
