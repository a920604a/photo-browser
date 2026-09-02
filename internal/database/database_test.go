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
	for _, table := range []string{"categories", "albums", "photos", "scan_runs", "users"} {
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

func TestUsersPartialUniqueIndexes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	insert := func(uid, email string) error {
		var uidV, emailV, normV interface{}
		if uid != "" {
			uidV = uid
		}
		if email != "" {
			emailV = email
			normV = strings.ToLower(email)
		}
		_, err := db.Exec(
			`INSERT INTO users(firebase_uid,email,normalized_email,role,enabled,created_at,updated_at)
			 VALUES(?,?,?,?,1,?,?)`,
			uidV, emailV, normV, "member", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z",
		)
		return err
	}

	if err := insert("uid-1", ""); err != nil {
		t.Fatalf("first uid insert: %v", err)
	}
	if err := insert("uid-1", ""); err == nil {
		t.Fatal("duplicate firebase_uid must fail")
	}
	if err := insert("", "alice@example.com"); err != nil {
		t.Fatalf("email insert: %v", err)
	}
	if err := insert("", "alice@example.com"); err == nil {
		t.Fatal("duplicate normalized_email must fail")
	}
	// null uid and null email combos: many nulls allowed by partial index, but
	// row itself needs at least one of the two (CHECK constraint).
	if err := insert("", ""); err == nil {
		t.Fatal("row with neither uid nor email must fail check")
	}
}
