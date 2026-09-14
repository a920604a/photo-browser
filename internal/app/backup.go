package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RunBackup writes a consistent snapshot of the live database with
// VACUUM INTO. Copying the .db file directly is unsafe under WAL — the
// snapshot would miss whatever is still in the write-ahead log.
func RunBackup(ctx context.Context, db *sql.DB, outPath string, stdout io.Writer) error {
	if outPath == "" {
		return fmt.Errorf("backup: --out is required")
	}
	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf("backup: %s already exists", outPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		return fmt.Errorf("backup: mkdir: %w", err)
	}
	// VACUUM INTO takes no bind parameters, so the path is quoted by hand.
	quoted := "'" + strings.ReplaceAll(outPath, "'", "''") + "'"
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+quoted); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	fmt.Fprintf(stdout, "backup: wrote %s (%d bytes)\n", outPath, info.Size())
	return nil
}
