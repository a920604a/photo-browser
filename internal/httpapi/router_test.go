package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
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

func buildRouter(t *testing.T, allowedOrigins []string) (http.Handler, *auth.TestSigner, *users.Store) {
	t.Helper()
	signer := auth.NewTestSigner(kid)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(signer.JWKS())
	}))
	t.Cleanup(srv.Close)
	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	us := users.NewStore(db)
	cs := catalog.NewStore(db)
	v := &auth.Verifier{
		Issuer: iss, Audience: aud, JWKSURL: srv.URL,
		Now: time.Now, Refresh: time.Hour,
	}
	return httpapi.NewRouter(httpapi.RouterDeps{
		Config:   config.Config{AllowedOrigins: allowedOrigins},
		Users:    us,
		Catalog:  cs,
		Verifier: v,
		Now:      time.Now,
	}), signer, us
}

func TestHealthLive(t *testing.T) {
	router, _, _ := buildRouter(t, nil)
	req := httptest.NewRequest("GET", "/api/v1/health/live", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("body=%s", rec.Body)
	}
}

func TestHealthReadyOK(t *testing.T) {
	router, _, _ := buildRouter(t, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health/ready", nil))
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestMeRequiresAuth(t *testing.T) {
	router, _, _ := buildRouter(t, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/me", nil))
	if rec.Code != 401 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestMeReturnsProfile(t *testing.T) {
	router, signer, us := buildRouter(t, nil)
	_, err := us.Add(context.Background(), users.User{
		FirebaseUID: sql.NullString{String: "uid-a", Valid: true},
		Role:        "admin", Enabled: true,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tok := signer.Sign(map[string]any{
		"iss": iss, "aud": aud, "sub": "uid-a",
		"email_verified": true, "email": "alice@example.com",
		"iat": time.Now().Unix() - 10, "exp": time.Now().Unix() + 3600,
	})
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["uid"] != "uid-a" || body["role"] != "admin" {
		t.Fatalf("body=%+v", body)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("router did not attach X-Request-ID")
	}
}

func TestMePreflightCORS(t *testing.T) {
	router, _, _ := buildRouter(t, []string{"https://ok.example"})
	req := httptest.NewRequest("OPTIONS", "/api/v1/me", nil)
	req.Header.Set("Origin", "https://ok.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://ok.example" {
		t.Fatalf("acao=%q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}
