package app_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"photo-browser/internal/app"
)

func TestRunServeGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.RunServe(ctx, app.ServeDeps{Router: router, Listener: ln}, io.Discard, io.Discard)
	}()

	// Confirm server is answering
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + ln.Addr().String() + "/anything")
		if err == nil {
			resp.Body.Close()
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		cancel()
		<-done
		t.Fatalf("serve never accepted: %v", lastErr)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunServe err=%v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("RunServe did not shut down within 6s")
	}
}

func TestRunServeRequiresRouter(t *testing.T) {
	err := app.RunServe(context.Background(), app.ServeDeps{}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error for missing router")
	}
}
