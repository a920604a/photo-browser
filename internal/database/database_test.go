package database

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesSchemaAndPragmas(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"categories", "albums", "photos", "scan_runs"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil || name != table {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys=%d err=%v", fk, err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.db")
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		db.Close()
	}
}
