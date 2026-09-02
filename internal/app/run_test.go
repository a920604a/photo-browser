package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"photo-browser/internal/filelock"
	"photo-browser/internal/indexer"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func okLock() (io.Closer, error) { return nopCloser{}, nil }

func TestRunIndexNormal(t *testing.T) {
	var seen indexer.Options
	called := false
	cmds := Commands{
		Index: func(ctx context.Context, o indexer.Options) error {
			seen = o
			called = true
			return nil
		},
		Lock: okLock,
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), []string{"index"}, stdout, stderr, cmds); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !called {
		t.Fatal("Index not called")
	}
	if seen.RebuildThumbnails {
		t.Fatalf("unexpected RebuildThumbnails=true")
	}
}

func TestRunIndexRebuildThumbnails(t *testing.T) {
	var seen indexer.Options
	cmds := Commands{
		Index: func(ctx context.Context, o indexer.Options) error { seen = o; return nil },
		Lock:  okLock,
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), []string{"index", "--rebuild-thumbnails"}, stdout, stderr, cmds); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !seen.RebuildThumbnails {
		t.Fatal("expected RebuildThumbnails=true")
	}
}

func TestRunRebuild(t *testing.T) {
	called := false
	cmds := Commands{
		Rebuild: func(ctx context.Context) error { called = true; return nil },
		Lock:    okLock,
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), []string{"rebuild"}, stdout, stderr, cmds); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !called {
		t.Fatal("Rebuild not called")
	}
}

func TestRunNoArgs(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), nil, stdout, stderr, Commands{}); code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"garbage"}, stdout, stderr, Commands{})
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

func TestRunIndexExtraArg(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"index", "--nope"}, stdout, stderr, Commands{Lock: okLock})
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

func TestRunRebuildExtraArg(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"rebuild", "extra"}, stdout, stderr, Commands{Lock: okLock})
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

func TestRunLockBusy(t *testing.T) {
	cmds := Commands{
		Index: func(ctx context.Context, o indexer.Options) error { return nil },
		Lock:  func() (io.Closer, error) { return nil, filelock.ErrAlreadyLocked },
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"index"}, stdout, stderr, cmds)
	if code != 3 {
		t.Fatalf("exit=%d want 3", code)
	}
	if !strings.Contains(stderr.String(), "already running") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestRunRuntimeFailure(t *testing.T) {
	cmds := Commands{
		Index: func(ctx context.Context, o indexer.Options) error { return errors.New("disk full") },
		Lock:  okLock,
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"index"}, stdout, stderr, cmds)
	if code != 1 {
		t.Fatalf("exit=%d want 1", code)
	}
	if !strings.Contains(stderr.String(), "disk full") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
