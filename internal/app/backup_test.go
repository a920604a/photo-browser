package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"photo-browser/internal/app"
	"photo-browser/internal/database"
)

func TestBackupProducesReadableCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.db")
	db, err := database.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users (firebase_uid, role, enabled, created_at, updated_at)
	                      VALUES ('uid-a','admin',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "backup", "photo-2026-01-01.db")
	var stdout bytes.Buffer
	if err := app.RunBackup(context.Background(), db, out, &stdout); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("backup file is empty")
	}

	// The copy must be a usable database, not just bytes on disk.
	restored, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var n int
	if err := restored.QueryRow(`SELECT count(*) FROM users WHERE firebase_uid='uid-a'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("restored user count=%d want 1", n)
	}
}

func TestBackupRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := filepath.Join(dir, "b.db")
	if err := os.WriteFile(out, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := app.RunBackup(context.Background(), db, out, &stdout); err == nil {
		t.Fatal("expected an error rather than clobbering an existing backup")
	}
}

func TestBackupRequiresOutPath(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var stdout bytes.Buffer
	if err := app.RunBackup(context.Background(), db, "", &stdout); err == nil {
		t.Fatal("expected an error when --out is missing")
	}
}
