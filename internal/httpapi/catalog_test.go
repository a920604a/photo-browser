package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
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

type seedResult struct {
	router  http.Handler
	signer  *auth.TestSigner
	users   *users.Store
	catalog *catalog.Store
	catID   int64
	albumID int64
	photoID int64
}

func seedCatalog(t *testing.T) *seedResult {
	t.Helper()
	signer := auth.NewTestSigner(kid)
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(signer.JWKS())
	}))
	t.Cleanup(jwks.Close)

	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cs := catalog.NewStore(db)
	us := users.NewStore(db)

	if _, err := us.Add(context.Background(), users.User{
		FirebaseUID: sql.NullString{String: "uid-a", Valid: true},
		Role:        "member", Enabled: true,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	scanID, err := cs.StartScan(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	catID, _ := cs.UpsertCategory(context.Background(), "travel", "travel", scanID, time.Now())
	albumID, _ := cs.UpsertAlbum(context.Background(), catID, "japan", "travel/japan", scanID, time.Now())
	var firstID int64
	for i := 0; i < 3; i++ {
		id, err := cs.InsertPhoto(context.Background(), catalog.Photo{
			AlbumID: albumID, Filename: fmt.Sprintf("p%d.jpg", i),
			RelativePath: fmt.Sprintf("travel/japan/p%d.jpg", i),
			MIMEType:     "image/jpeg", FileSize: int64(100 + i), FileMTimeNS: int64(i),
			TakenAt:      sql.NullString{String: fmt.Sprintf("2024-01-0%dT00:00:00", i+1), Valid: true},
			ThumbnailKey: sql.NullString{String: fmt.Sprintf("k-%d", i), Valid: true},
		}, scanID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if firstID == 0 {
			firstID = id
		}
	}
	_ = cs.FinishScan(context.Background(), scanID, "succeeded", catalog.ScanCounts{Seen: 3, New: 3}, "", time.Now())

	v := &auth.Verifier{
		Issuer: iss, Audience: aud, JWKSURL: jwks.URL,
		Now: time.Now, Refresh: time.Hour,
	}
	router := httpapi.NewRouter(httpapi.RouterDeps{
		Config: config.Config{}, Users: us, Catalog: cs, Verifier: v, Now: time.Now,
	})
	return &seedResult{router: router, signer: signer, users: us, catalog: cs,
		catID: catID, albumID: albumID, photoID: firstID}
}

func memberToken(f *seedResult) string {
	return f.signer.Sign(map[string]any{
		"iss": iss, "aud": aud, "sub": "uid-a",
		"email": "alice@example.com", "email_verified": true,
		"iat": time.Now().Unix() - 10, "exp": time.Now().Unix() + 3600,
	})
}

func doGET(t *testing.T, router http.Handler, tok, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestCatalogEndpointsRequireAuth(t *testing.T) {
	f := seedCatalog(t)
	for _, p := range []string{
		"/api/v1/categories",
		"/api/v1/albums",
		"/api/v1/photos",
		"/api/v1/photos/timeline",
	} {
		rec := doGET(t, f.router, "", p)
		if rec.Code != 401 {
			t.Errorf("%s: code=%d want 401", p, rec.Code)
		}
	}
}

func TestListCategories(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f), "/api/v1/categories")
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body struct{ Items []map[string]any }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0]["name"] != "travel" {
		t.Fatalf("items=%v", body.Items)
	}
}

func TestListCategoryAlbums(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/categories/%d/albums", f.catID))
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body struct{ Items []map[string]any }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items=%v", body.Items)
	}
	assertAlbumCover(t, body.Items[0])
}

func TestListAlbumsIncludesCover(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f), "/api/v1/albums")
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body struct{ Items []map[string]any }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items=%v", body.Items)
	}
	assertAlbumCover(t, body.Items[0])
}

// assertAlbumCover checks an album DTO carries both cover fields the web client
// needs to build a thumbnail URL without a second request.
func assertAlbumCover(t *testing.T, album map[string]any) {
	t.Helper()
	id, ok := album["cover_photo_id"].(float64)
	if !ok || id == 0 {
		t.Fatalf("missing cover_photo_id: %v", album)
	}
	if key, _ := album["cover_thumbnail_key"].(string); key == "" {
		t.Fatalf("missing cover_thumbnail_key: %v", album)
	}
}

func TestListCategoryAlbumsUnknownCategory(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f), "/api/v1/categories/999999/albums")
	if rec.Code != 404 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestGetAlbumIncludesCover(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/albums/%d", f.albumID))
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	assertAlbumCover(t, body)
}

func TestListAlbumPhotosPaginates(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/albums/%d/photos?limit=2", f.albumID))
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Items      []map[string]any
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Items) != 2 || body.NextCursor == "" {
		t.Fatalf("body=%+v", body)
	}
}

func TestListAlbumPhotosBadCursor(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/albums/%d/photos?cursor=!!!", f.albumID))
	if rec.Code != 400 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestListPhotosTimeline(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f), "/api/v1/photos/timeline?limit=100")
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestListPhotosBadLimit(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f), "/api/v1/photos?limit=abc")
	if rec.Code != 400 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestGetPhoto(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f),
		fmt.Sprintf("/api/v1/photos/%d", f.photoID))
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["thumbnail_key"] == "" {
		t.Fatalf("thumbnail_key missing: %v", body)
	}
}

func TestGetPhotoMissing(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f), "/api/v1/photos/999999")
	if rec.Code != 404 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestGetPhotoInvalidID(t *testing.T) {
	f := seedCatalog(t)
	rec := doGET(t, f.router, memberToken(f), "/api/v1/photos/abc")
	if rec.Code != 400 {
		t.Fatalf("code=%d", rec.Code)
	}
}
