package app_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"photo-browser/internal/app"
)

func TestHealthcheckOKWhenReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health/ready" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	var stderr bytes.Buffer
	code := app.RunHealthcheck(context.Background(), srv.URL+"/api/v1/health/ready", srv.Client(), &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestHealthcheckFailsOnNotReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	var stderr bytes.Buffer
	if code := app.RunHealthcheck(context.Background(), srv.URL+"/api/v1/health/ready", srv.Client(), &stderr); code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected a diagnostic on stderr")
	}
}

func TestHealthcheckFailsWhenUnreachable(t *testing.T) {
	var stderr bytes.Buffer
	// Port 1 is never listening; this must fail fast rather than hang.
	if code := app.RunHealthcheck(context.Background(), "http://127.0.0.1:1/api/v1/health/ready", http.DefaultClient, &stderr); code != 1 {
		t.Fatalf("code=%d want 1", code)
	}
}
