package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"photo-browser/internal/httpapi"
)

func TestRequestIDGeneratedWhenAbsent(t *testing.T) {
	var seen string
	h := httpapi.WithRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = httpapi.RequestIDFromContext(r.Context())
		w.WriteHeader(204)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if seen == "" {
		t.Fatal("request id not populated in context")
	}
	if got := rec.Header().Get("X-Request-ID"); got != seen {
		t.Fatalf("response header=%q context=%q", got, seen)
	}
}

func TestRequestIDReusedWhenSafe(t *testing.T) {
	h := httpapi.WithRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got != "abc-123" {
		t.Fatalf("header=%q", got)
	}
}

func TestRequestIDRejectsBadPattern(t *testing.T) {
	h := httpapi.WithRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	got := rec.Header().Get("X-Request-ID")
	if got == "" || got == "bad id with spaces" {
		t.Fatalf("expected regenerated id, got %q", got)
	}
}
