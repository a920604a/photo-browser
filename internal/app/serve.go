package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"photo-browser/internal/auth"
	"photo-browser/internal/httpapi"
)

// ServeDeps is what RunServe needs from the caller. Tests inject fakes;
// Main builds it from prodEnv.
type ServeDeps struct {
	Listen   string
	Router   http.Handler
	Listener net.Listener // optional: pre-bound listener for tests
}

// RunServe binds the HTTP listener and blocks until ctx is done, then
// gracefully shuts down. Returns non-nil error on bind or shutdown failure.
func RunServe(ctx context.Context, d ServeDeps, stdout, stderr io.Writer) error {
	if d.Router == nil {
		return errors.New("serve: router not wired")
	}
	srv := &http.Server{
		Handler:           d.Router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ln := d.Listener
	if ln == nil {
		l, err := net.Listen("tcp", d.Listen)
		if err != nil {
			return fmt.Errorf("listen %q: %w", d.Listen, err)
		}
		ln = l
	}
	fmt.Fprintf(stdout, "listening on %s\n", ln.Addr().String())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// serveFromEnv wires the production router from prodEnv. Called by Main.
func serveFromEnv(ctx context.Context, env *prodEnv, stdout, stderr io.Writer) error {
	if env.cfg.FirebaseProjectID == "" {
		return errors.New("FIREBASE_PROJECT_ID is required for serve")
	}
	if _, err := env.indexer(); err != nil {
		return err
	}
	us, err := env.usersStore()
	if err != nil {
		return err
	}
	v := &auth.Verifier{
		Issuer:   env.cfg.FirebaseIssuer,
		Audience: env.cfg.FirebaseProjectID,
		JWKSURL:  env.cfg.FirebaseJWKSURL,
		Refresh:  time.Hour,
	}
	router := httpapi.NewRouter(httpapi.RouterDeps{
		Config:   env.cfg,
		Users:    us,
		Catalog:  env.store,
		Verifier: v,
		Indexer:  env.idx,
		Now:      time.Now,
	})
	return RunServe(ctx, ServeDeps{Listen: env.cfg.HTTPListen, Router: router}, stdout, stderr)
}
