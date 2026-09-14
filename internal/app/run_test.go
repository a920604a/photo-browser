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

func TestRunAdminDispatch(t *testing.T) {
	var seenArgs []string
	cmds := Commands{
		Admin: func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
			seenArgs = args
			return 0
		},
		Lock: okLock,
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"admin", "list-users"}, stdout, stderr, cmds)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if len(seenArgs) != 1 || seenArgs[0] != "list-users" {
		t.Fatalf("args=%v", seenArgs)
	}
}

func TestRunAdminUnwired(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"admin"}, stdout, stderr, Commands{Lock: okLock})
	if code != 1 {
		t.Fatalf("exit=%d want 1", code)
	}
}

func TestRunAdminLockBusy(t *testing.T) {
	cmds := Commands{
		Admin: func(ctx context.Context, args []string, stdout, stderr io.Writer) int { return 0 },
		Lock:  func() (io.Closer, error) { return nil, filelock.ErrAlreadyLocked },
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"admin", "list-users"}, stdout, stderr, cmds)
	if code != 3 {
		t.Fatalf("exit=%d want 3", code)
	}
}

func TestRunServeDispatchesAndReleasesLock(t *testing.T) {
	served := make(chan struct{}, 1)
	cmds := Commands{
		Serve: func(ctx context.Context, stdout, stderr io.Writer) error {
			served <- struct{}{}
			return nil
		},
		ServeLock: okLock,
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"serve"}, stdout, stderr, cmds)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	select {
	case <-served:
	default:
		t.Fatal("Serve not called")
	}
}

func TestRunServeUnwired(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"serve"}, stdout, stderr, Commands{})
	if code != 1 {
		t.Fatalf("exit=%d want 1", code)
	}
}

func TestRunServeExtraArg(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"serve", "extra"}, stdout, stderr, Commands{
		Serve:     func(ctx context.Context, stdout, stderr io.Writer) error { return nil },
		ServeLock: okLock,
	})
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

func TestRunServeLockBusy(t *testing.T) {
	cmds := Commands{
		Serve:     func(ctx context.Context, stdout, stderr io.Writer) error { return nil },
		ServeLock: func() (io.Closer, error) { return nil, filelock.ErrAlreadyLocked },
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"serve"}, stdout, stderr, cmds)
	if code != 3 {
		t.Fatalf("exit=%d want 3", code)
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

func TestRunHealthcheckDispatches(t *testing.T) {
	called := false
	code := Run(context.Background(), []string{"healthcheck"}, io.Discard, io.Discard, Commands{
		Healthcheck: func(context.Context, []string, io.Writer, io.Writer) int { called = true; return 0 },
	})
	if code != 0 || !called {
		t.Fatalf("code=%d called=%v", code, called)
	}
}

func TestRunHealthcheckTakesNoLock(t *testing.T) {
	code := Run(context.Background(), []string{"healthcheck"}, io.Discard, io.Discard, Commands{
		Lock: func() (io.Closer, error) {
			t.Error("healthcheck must not take the index lock")
			return nopCloser{}, nil
		},
		Healthcheck: func(context.Context, []string, io.Writer, io.Writer) int { return 0 },
	})
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
}

func TestRunHealthcheckRejectsUnknownFlag(t *testing.T) {
	code := Run(context.Background(), []string{"healthcheck", "--nope"}, io.Discard, io.Discard, Commands{
		Healthcheck: func(_ context.Context, args []string, _, stderr io.Writer) int {
			// Main's closure does the flag parsing; here we only assert the
			// arguments reach it untouched.
			if len(args) != 1 || args[0] != "--nope" {
				t.Errorf("args=%v", args)
			}
			return 2
		},
	})
	if code != 2 {
		t.Fatalf("code=%d want 2", code)
	}
}

func TestRunBackupTakesTheIndexLock(t *testing.T) {
	locked := false
	code := Run(context.Background(), []string{"backup", "--out=/tmp/x.db"}, io.Discard, io.Discard, Commands{
		Lock:   func() (io.Closer, error) { locked = true; return nopCloser{}, nil },
		Backup: func(context.Context, []string, io.Writer, io.Writer) int { return 0 },
	})
	if code != 0 || !locked {
		t.Fatalf("code=%d locked=%v", code, locked)
	}
}

func TestRunBackupReportsLockBusy(t *testing.T) {
	code := Run(context.Background(), []string{"backup", "--out=/tmp/x.db"}, io.Discard, io.Discard, Commands{
		Lock: func() (io.Closer, error) { return nil, filelock.ErrAlreadyLocked },
		Backup: func(context.Context, []string, io.Writer, io.Writer) int {
			t.Error("backup ran despite a busy lock")
			return 0
		},
	})
	if code != 3 {
		t.Fatalf("code=%d want 3", code)
	}
}
