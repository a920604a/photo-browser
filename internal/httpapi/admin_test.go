package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"photo-browser/internal/auth"
	"photo-browser/internal/catalog"
	"photo-browser/internal/config"
	"photo-browser/internal/database"
	"photo-browser/internal/httpapi"
	"photo-browser/internal/indexer"
	"photo-browser/internal/media"
	"photo-browser/internal/scanner"
	"photo-browser/internal/users"
)

type adminFixture struct {
	router  http.Handler
	signer  *auth.TestSigner
	catalog *catalog.Store
	users   *users.Store
	scans   *int32
	indexer *indexer.Indexer
}

// noopThumbnail satisfies indexer.ThumbnailFn without touching disk.
func noopThumbnail(ctx context.Context, source, key string, srcMaxDim int) error { return nil }

// nowalkFn returns no entries so indexer completes immediately.
func nowalkFn(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
	return nil, nil
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	signer := auth.NewTestSigner(kid)
	jwks := httptest.NewServer(newJWKSHandler(signer))
	t.Cleanup(jwks.Close)

	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cs := catalog.NewStore(db)
	us := users.NewStore(db)

	_, _ = us.Add(context.Background(), users.User{
		FirebaseUID: sql.NullString{String: "admin-uid", Valid: true},
		Role:        "admin", Enabled: true,
	}, time.Now())
	_, _ = us.Add(context.Background(), users.User{
		FirebaseUID: sql.NullString{String: "member-uid", Valid: true},
		Role:        "member", Enabled: true,
	}, time.Now())

	var scanCount int32
	idx := &indexer.Indexer{
		Store:        cs,
		PhotoRoot:    dir,
		ThumbnailDir: dir,
		Walk: func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
			atomic.AddInt32(&scanCount, 1)
			return nowalkFn(root, warn)
		},
		ReadMetadata: func(p string) (media.Metadata, error) { return media.Metadata{}, nil },
		Thumbnail:    noopThumbnail,
	}
	v := &auth.Verifier{Issuer: iss, Audience: aud, JWKSURL: jwks.URL,
		Now: time.Now, Refresh: time.Hour}
	router := httpapi.NewRouter(httpapi.RouterDeps{
		Config: config.Config{}, Users: us, Catalog: cs, Verifier: v, Indexer: idx, Now: time.Now,
	})
	return &adminFixture{router: router, signer: signer, catalog: cs, users: us, scans: &scanCount, indexer: idx}
}

func adminToken(f *adminFixture) string {
	return f.signer.Sign(map[string]any{
		"iss": iss, "aud": aud, "sub": "admin-uid",
		"email_verified": true,
		"iat":            time.Now().Unix() - 10, "exp": time.Now().Unix() + 3600,
	})
}

func adminMemberToken(f *adminFixture) string {
	return f.signer.Sign(map[string]any{
		"iss": iss, "aud": aud, "sub": "member-uid",
		"email_verified": true,
		"iat":            time.Now().Unix() - 10, "exp": time.Now().Unix() + 3600,
	})
}

func doPOST(t *testing.T, router http.Handler, tok, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func doPATCH(t *testing.T, router http.Handler, tok, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PATCH", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAdminUsersRequiresAdmin(t *testing.T) {
	f := newAdminFixture(t)
	rec := doGET(t, f.router, adminMemberToken(f), "/api/v1/admin/users")
	if rec.Code != 403 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestAdminListUsers(t *testing.T) {
	f := newAdminFixture(t)
	rec := doGET(t, f.router, adminToken(f), "/api/v1/admin/users")
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Items []map[string]any
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Items) != 2 {
		t.Fatalf("items=%v", body.Items)
	}
}

func TestAdminAddUser(t *testing.T) {
	f := newAdminFixture(t)
	rec := doPOST(t, f.router, adminToken(f), "/api/v1/admin/users",
		`{"firebase_uid":"new-uid","role":"member"}`)
	if rec.Code != 201 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	rec = doPOST(t, f.router, adminToken(f), "/api/v1/admin/users",
		`{"firebase_uid":"new-uid","role":"member"}`)
	if rec.Code != 409 {
		t.Fatalf("dup code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestAdminAddUserMissingBoth(t *testing.T) {
	f := newAdminFixture(t)
	rec := doPOST(t, f.router, adminToken(f), "/api/v1/admin/users", `{"role":"member"}`)
	if rec.Code != 400 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestAdminPatchRoleAndEnable(t *testing.T) {
	f := newAdminFixture(t)
	// find the member user's id
	list, _ := f.users.List(context.Background())
	var target int64
	for _, u := range list {
		if u.Role == "member" {
			target = u.ID
		}
	}
	if target == 0 {
		t.Fatal("no seeded member")
	}
	rec := doPATCH(t, f.router, adminToken(f),
		fmt.Sprintf("/api/v1/admin/users/%d", target),
		`{"role":"admin","enabled":false}`)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	got, _, _ := f.users.LookupByUID(context.Background(), "member-uid")
	if got.Role != "admin" || got.Enabled {
		t.Fatalf("state=%+v", got)
	}
}

func TestAdminPatchUnknown(t *testing.T) {
	f := newAdminFixture(t)
	rec := doPATCH(t, f.router, adminToken(f), "/api/v1/admin/users/999999", `{"role":"admin"}`)
	if rec.Code != 404 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestAdminStartIndexRunReturns202(t *testing.T) {
	f := newAdminFixture(t)
	rec := doPOST(t, f.router, adminToken(f), "/api/v1/admin/index-runs", `{}`)
	if rec.Code != 202 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body struct{ ScanID int64 `json:"scan_id"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.ScanID == 0 {
		t.Fatalf("scan_id missing: %s", rec.Body)
	}

	// Wait for background scan to finish
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, ok, err := f.catalog.GetScanRun(context.Background(), body.ScanID)
		if err == nil && ok && run.Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec = doGET(t, f.router, adminToken(f),
		fmt.Sprintf("/api/v1/admin/index-runs/%d", body.ScanID))
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if atomic.LoadInt32(f.scans) != 1 {
		t.Fatalf("expected 1 scan invocation, got %d", atomic.LoadInt32(f.scans))
	}
}

func TestAdminIndexRunConflict(t *testing.T) {
	f := newAdminFixture(t)
	// Replace Walk with one that blocks on a channel to hold the semaphore
	release := make(chan struct{})
	f.indexer.Walk = func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error) {
		<-release
		return nil, nil
	}
	rec := doPOST(t, f.router, adminToken(f), "/api/v1/admin/index-runs", `{}`)
	if rec.Code != 202 {
		t.Fatalf("first code=%d", rec.Code)
	}
	rec = doPOST(t, f.router, adminToken(f), "/api/v1/admin/index-runs", `{}`)
	if rec.Code != 409 {
		close(release)
		t.Fatalf("second code=%d body=%s", rec.Code, rec.Body)
	}
	close(release)
	// Allow the goroutine to release the semaphore before test ends.
	time.Sleep(50 * time.Millisecond)
}

func TestGetIndexRunUnknown(t *testing.T) {
	f := newAdminFixture(t)
	rec := doGET(t, f.router, adminToken(f), "/api/v1/admin/index-runs/999999")
	if rec.Code != 404 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}
