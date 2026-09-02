package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"photo-browser/internal/httpapi"
)

func newNext() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
}

func TestCORSPreflightAllowed(t *testing.T) {
	h := httpapi.WithCORS([]string{"https://ok.example"}, newNext())
	req := httptest.NewRequest("OPTIONS", "/api/v1/photos", nil)
	req.Header.Set("Origin", "https://ok.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://ok.example" {
		t.Fatalf("acao=%q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("vary=%q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("creds=%q", got)
	}
}

func TestCORSPreflightRejected(t *testing.T) {
	h := httpapi.WithCORS([]string{"https://ok.example"}, newNext())
	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "https://bad.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("must not set ACAO for unknown origin")
	}
}

func TestCORSGetFromForeignOriginPasses(t *testing.T) {
	h := httpapi.WithCORS([]string{"https://ok.example"}, newNext())
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Origin", "https://foreign.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("must not send ACAO to unlisted origin")
	}
}

func TestCORSEmptyAllowlistPassthrough(t *testing.T) {
	h := httpapi.WithCORS(nil, newNext())
	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "https://anything.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// pass-through means next handler (200) runs even for preflight
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
}
