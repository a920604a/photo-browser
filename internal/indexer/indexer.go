package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"photo-browser/internal/catalog"
	"photo-browser/internal/media"
	"photo-browser/internal/scanner"
)

const DefaultBatchSize = 200

type Options struct {
	RebuildThumbnails bool
}

type WalkFn func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error)
type ReadMetadataFn func(path string) (media.Metadata, error)
type ThumbnailFn func(ctx context.Context, source, key string) error

type Indexer struct {
	Store        *catalog.Store
	PhotoRoot    string
	ThumbnailDir string
	Walk         WalkFn
	ReadMetadata ReadMetadataFn
	Thumbnail    ThumbnailFn
	BatchSize    int
	Now          func() time.Time
}

// ThumbnailKey formats the deterministic key for a photo's thumbnail file.
func ThumbnailKey(photoID, mtimeNS, size int64) string {
	return fmt.Sprintf("%d-%d-%d", photoID, mtimeNS, size)
}

// Run performs one full scan cycle. The caller receives counts even on error.
func (idx *Indexer) Run(ctx context.Context, opts Options) (counts catalog.ScanCounts, runErr error) {
	now := idx.now()
	scanID, err := idx.Store.StartScan(ctx, now)
	if err != nil {
		return counts, err
	}
	succeeded := false
	defer func() {
		status := "failed"
		summary := ""
		if runErr != nil {
			summary = runErr.Error()
		}
		if succeeded {
			status = "succeeded"
			summary = ""
		}
		_ = idx.Store.FinishScan(ctx, scanID, status, counts, summary, idx.now())
	}()

	entries, walkErr := idx.Walk(idx.PhotoRoot, func(w scanner.Warning) {
		counts.Warnings++
	})
	if walkErr != nil {
		return counts, walkErr
	}
	counts.Seen = int64(len(entries))

	batchSize := idx.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	for start := 0; start < len(entries); start += batchSize {
		end := start + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[start:end]
		err := idx.Store.InTx(ctx, func(s *catalog.Store) error {
			for i := range batch {
				if err := idx.processEntry(ctx, s, &batch[i], scanID, opts, now, &counts); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return counts, err
		}
	}
	if err := idx.reconcile(ctx, scanID, &counts); err != nil {
		return counts, err
	}
	succeeded = true
	return counts, nil
}

func (idx *Indexer) processEntry(
	ctx context.Context,
	s *catalog.Store,
	entry *scanner.Entry,
	scanID int64,
	opts Options,
	now time.Time,
	counts *catalog.ScanCounts,
) error {
	catID, err := s.UpsertCategory(ctx, entry.CategoryName, entry.CategoryPath, scanID, now)
	if err != nil {
		return err
	}
	albumID, err := s.UpsertAlbum(ctx, catID, entry.AlbumName, entry.AlbumPath, scanID, now)
	if err != nil {
		return err
	}
	existing, found, err := s.FindPhoto(ctx, entry.RelativePath)
	if err != nil {
		return err
	}
	src := filepath.Join(idx.PhotoRoot, filepath.FromSlash(entry.RelativePath))

	if !found {
		return idx.handleNew(ctx, s, entry, albumID, scanID, src, now, counts)
	}
	if entry.Size != existing.FileSize || entry.MTimeNS != existing.FileMTimeNS {
		return idx.handleChanged(ctx, s, entry, existing, scanID, src, now, counts)
	}
	return idx.handleUnchanged(ctx, s, entry, existing, opts, scanID, src, now, counts)
}

func (idx *Indexer) handleNew(
	ctx context.Context,
	s *catalog.Store,
	entry *scanner.Entry,
	albumID, scanID int64,
	src string,
	now time.Time,
	counts *catalog.ScanCounts,
) error {
	meta, metaErr := idx.ReadMetadata(src)
	p := catalog.Photo{
		AlbumID:      albumID,
		Filename:     entry.Filename,
		RelativePath: entry.RelativePath,
		FileSize:     entry.Size,
		FileMTimeNS:  entry.MTimeNS,
	}
	if metaErr != nil {
		counts.Warnings++
		p.MIMEType = fallbackMIME(entry.Filename)
	} else {
		p.MIMEType = meta.MIMEType
		p.Width = meta.Width
		p.Height = meta.Height
		p.TakenAt = meta.TakenAt
	}
	id, err := s.InsertPhoto(ctx, p, scanID, now)
	if err != nil {
		return err
	}
	counts.New++
	if metaErr != nil {
		return nil // no thumbnail if we couldn't decode
	}
	key := ThumbnailKey(id, entry.MTimeNS, entry.Size)
	if err := idx.Thumbnail(ctx, src, key); err != nil {
		counts.Warnings++
		return nil
	}
	return s.UpdateThumbnailKey(ctx, id, sql.NullString{String: key, Valid: true}, now)
}

func (idx *Indexer) handleChanged(
	ctx context.Context,
	s *catalog.Store,
	entry *scanner.Entry,
	existing catalog.Photo,
	scanID int64,
	src string,
	now time.Time,
	counts *catalog.ScanCounts,
) error {
	meta, metaErr := idx.ReadMetadata(src)
	existing.FileSize = entry.Size
	existing.FileMTimeNS = entry.MTimeNS

	if metaErr != nil {
		counts.Warnings++
		existing.ThumbnailKey = sql.NullString{}
		counts.Changed++
		return s.UpdatePhoto(ctx, existing, scanID, now)
	}

	existing.MIMEType = meta.MIMEType
	existing.Width = meta.Width
	existing.Height = meta.Height
	existing.TakenAt = meta.TakenAt

	newKey := ThumbnailKey(existing.ID, entry.MTimeNS, entry.Size)
	if err := idx.Thumbnail(ctx, src, newKey); err != nil {
		counts.Warnings++
		existing.ThumbnailKey = sql.NullString{}
	} else {
		existing.ThumbnailKey = sql.NullString{String: newKey, Valid: true}
	}
	counts.Changed++
	return s.UpdatePhoto(ctx, existing, scanID, now)
}

func (idx *Indexer) handleUnchanged(
	ctx context.Context,
	s *catalog.Store,
	entry *scanner.Entry,
	existing catalog.Photo,
	opts Options,
	scanID int64,
	src string,
	now time.Time,
	counts *catalog.ScanCounts,
) error {
	if opts.RebuildThumbnails {
		key := ThumbnailKey(existing.ID, entry.MTimeNS, entry.Size)
		if err := idx.Thumbnail(ctx, src, key); err != nil {
			counts.Warnings++
		} else if !existing.ThumbnailKey.Valid || existing.ThumbnailKey.String != key {
			if err := s.UpdateThumbnailKey(ctx, existing.ID, sql.NullString{String: key, Valid: true}, now); err != nil {
				return err
			}
		}
	}
	counts.Unchanged++
	return s.MarkPhotoSeen(ctx, existing.ID, scanID, now)
}

func (idx *Indexer) now() time.Time {
	if idx.Now != nil {
		return idx.Now()
	}
	return time.Now().UTC()
}

func fallbackMIME(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
