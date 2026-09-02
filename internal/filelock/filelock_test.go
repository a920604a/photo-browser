//go:build unix

package filelock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireBusyThenRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Acquire(path); !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("second acquire err=%v want ErrAlreadyLocked", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	third, err := Acquire(path)
	if err != nil {
		t.Fatalf("third acquire after release failed: %v", err)
	}
	third.Close()
}

func TestAcquireCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "created.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}
