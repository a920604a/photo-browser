package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"photo-browser/internal/catalog"
)

// RemoveThumbnail deletes {dir}/{key}.webp. A missing file is treated as
// success. The key must be a plain basename.
func RemoveThumbnail(dir, key string) error {
	if key == "" || key == "." || key == ".." || filepath.Base(key) != key {
		return fmt.Errorf("invalid thumbnail key %q", key)
	}
	path := filepath.Join(dir, key+".webp")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// reconcile deletes photos not marked seen in scanID, then removes empty
// albums/categories in the same transaction; only after commit does it delete
// the associated thumbnail files and any orphan .webp entries in ThumbnailDir.
func (idx *Indexer) reconcile(ctx context.Context, scanID int64, counts *catalog.ScanCounts) error {
	var obsoleteKeys []string
	var removed int64
	err := idx.Store.InTx(ctx, func(s *catalog.Store) error {
		keys, n, err := s.DeleteUnseen(ctx, scanID)
		if err != nil {
			return err
		}
		obsoleteKeys = keys
		removed = n
		if _, err := s.DeleteEmptyAlbums(ctx); err != nil {
			return err
		}
		if _, err := s.DeleteEmptyCategories(ctx); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	counts.Removed = removed

	for _, k := range obsoleteKeys {
		if err := RemoveThumbnail(idx.ThumbnailDir, k); err != nil {
			counts.Warnings++
		}
	}

	current, err := idx.Store.CurrentThumbnailKeys(ctx)
	if err != nil {
		return fmt.Errorf("list current keys: %w", err)
	}
	keep := make(map[string]struct{}, len(current))
	for _, k := range current {
		keep[k] = struct{}{}
	}
	if err := idx.cleanupOrphans(keep, counts); err != nil {
		return err
	}
	return nil
}

func (idx *Indexer) cleanupOrphans(keep map[string]struct{}, counts *catalog.ScanCounts) error {
	entries, err := os.ReadDir(idx.ThumbnailDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read thumbnail dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasSuffix(name, ".webp") {
			continue
		}
		key := strings.TrimSuffix(name, ".webp")
		if _, ok := keep[key]; ok {
			continue
		}
		if err := RemoveThumbnail(idx.ThumbnailDir, key); err != nil {
			counts.Warnings++
		}
	}
	return nil
}
