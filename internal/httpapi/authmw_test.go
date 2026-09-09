package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"photo-browser/internal/auth"
	"photo-browser/internal/database"
	"photo-browser/internal/httpapi"
	"photo-browser/internal/users"
)

const (
	iss = "https://securetoken.google.com/demo-proj"
	aud = "demo-proj"
	kid = "test-kid-1"
)

type authFixture struct {
	verifier *auth.Verifier
	signer   *auth.TestSigner
	users    *users.Store
	server   *httptest.Server
	jwksHits *int32
}

func (f *authFixture) close() { f.server.Close() }

func newAuthFixture(t *testing.T, now time.Time) *authFixture {
	t.Helper()
	signer := auth.NewTestSigner(kid)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(signer.JWKS())
	}))
	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &authFixture{
		verifier: &auth.Verifier{
			Issuer: iss, Audience: aud, JWKSURL: srv.URL,
			Now:     func() time.Time { return now },
			Refresh: time.Hour,
		},
		signer:   signer,
		users:    users.NewStore(db),
		server:   srv,
		jwksHits: &hits,
	}
}

func mint(f *authFixture, over map[string]any) string {
	c := map[string]any{
		"iss":            iss,
		"aud":            aud,
		"sub":            "uid-a",
		"email":          "alice@example.com",
		"email_verified": true,
		"iat":            time.Now().Unix() - 10,
		"exp":            time.Now().Unix() + 3600,
	}
	for k, v := range over {
		c[k] = v
	}
	return f.signer.Sign(c)
}

func passHandler(t *testing.T, wantRole string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := httpapi.UserFromContext(r.Context())
		if !ok {
			t.Errorf("user missing from context")
		}
		if wantRole != "" && u.Role != wantRole {
			t.Errorf("role=%q want %q", u.Role, wantRole)
		}
		w.WriteHeader(http.StatusOK)
	})
}

func addUser(t *testing.T, s *users.Store, u users.User) int64 {
	t.Helper()
	id, err := s.Add(context.Background(), u, time.Now())
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func TestWithAuthMissingHeader(t *testing.T) {
	f := newAuthFixture(t, time.Unix(1_700_000_000, 0))
	defer f.close()
	h := httpapi.WithAuth(f.verifier, f.users, passHandler(t, ""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("code=%d", rec.Code)
	}
	var body map[string]map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"]["code"] != "unauthorized" {
		t.Fatalf("body=%s", rec.Body)
	}
}

func TestWithAuthAllowsUIDInAllowlist(t *testing.T) {
	f := newAuthFixture(t, time.Now())
	defer f.close()
	addUser(t, f.users, users.User{
		FirebaseUID: sql.NullString{String: "uid-a", Valid: true},
		Role:        "member", Enabled: true,
	})
	h := httpapi.WithAuth(f.verifier, f.users, passHandler(t, "member"))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mint(f, nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestWithAuthDenyForeignUID(t *testing.T) {
	f := newAuthFixture(t, time.Now())
	defer f.close()
	h := httpapi.WithAuth(f.verifier, f.users, passHandler(t, ""))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mint(f, map[string]any{"sub": "unknown"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestWithAuthEmailAllowlistRequiresVerified(t *testing.T) {
	f := newAuthFixture(t, time.Now())
	defer f.close()
	addUser(t, f.users, users.User{
		Email:           sql.NullString{String: "alice@example.com", Valid: true},
		NormalizedEmail: sql.NullString{String: "alice@example.com", Valid: true},
		Role:            "member", Enabled: true,
	})
	h := httpapi.WithAuth(f.verifier, f.users, passHandler(t, "member"))
	// email_verified=false must fail closed even though the email matches
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mint(f, map[string]any{"sub": "unmapped", "email_verified": false}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("unverified email should 403, got %d", rec.Code)
	}
	// verified=true succeeds
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mint(f, map[string]any{"sub": "unmapped"}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("verified email should 200, got %d body=%s", rec.Code, rec.Body)
	}
}

func TestWithAuthDisabledUser(t *testing.T) {
	f := newAuthFixture(t, time.Now())
	defer f.close()
	addUser(t, f.users, users.User{
		FirebaseUID: sql.NullString{String: "uid-a", Valid: true},
		Role:        "member", Enabled: false,
	})
	h := httpapi.WithAuth(f.verifier, f.users, passHandler(t, ""))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mint(f, nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("disabled user must 403, got %d", rec.Code)
	}
}

func TestWithAuthExpiredToken(t *testing.T) {
	f := newAuthFixture(t, time.Unix(1_700_000_000, 0))
	defer f.close()
	addUser(t, f.users, users.User{
		FirebaseUID: sql.NullString{String: "uid-a", Valid: true},
		Role:        "member", Enabled: true,
	})
	tok := f.signer.Sign(map[string]any{
		"iss": iss, "aud": aud, "sub": "uid-a", "email_verified": true,
		"iat": 1_699_996_400, "exp": 1_699_996_500, // long expired vs Now
	})
	h := httpapi.WithAuth(f.verifier, f.users, passHandler(t, ""))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestRequireRoleGates(t *testing.T) {
	f := newAuthFixture(t, time.Now())
	defer f.close()
	addUser(t, f.users, users.User{
		FirebaseUID: sql.NullString{String: "uid-a", Valid: true},
		Role:        "member", Enabled: true,
	})
	h := httpapi.WithAuth(f.verifier, f.users,
		httpapi.RequireRole("admin", passHandler(t, "admin")))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mint(f, nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("member should 403 on admin gate, got %d", rec.Code)
	}
}

func TestBearerPrefixCaseSensitive(t *testing.T) {
	f := newAuthFixture(t, time.Now())
	defer f.close()
	h := httpapi.WithAuth(f.verifier, f.users, passHandler(t, ""))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "bearer "+mint(f, nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("lowercase bearer must be rejected, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Fatalf("body=%s", rec.Body)
	}
}
