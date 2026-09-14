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

	// skipThumbnails is decided once per run by thumbnailsAllowed and carried
	// to the per-entry handlers. Unexported so only a run can set it.
	skipThumbnails bool
}

type WalkFn func(root string, warn func(scanner.Warning)) ([]scanner.Entry, error)
type ReadMetadataFn func(path string) (media.Metadata, error)
type ThumbnailFn func(ctx context.Context, source, key string, srcMaxDim int) error

type Indexer struct {
	Store        *catalog.Store
	PhotoRoot    string
	ThumbnailDir string
	Walk         WalkFn
	ReadMetadata ReadMetadataFn
	Thumbnail    ThumbnailFn
	BatchSize    int
	Now          func() time.Time

	// Thumbnail generation stops when the thumbnail volume drops below
	// MinFreeBytes, so a filling disk costs thumbnails rather than the
	// catalogue or the originals. Zero disables the check.
	FreeSpace    func(path string) (uint64, error)
	MinFreeBytes int64

	// Optional sink for warnings. Without it warnings are only counted, which
	// is not enough to diagnose a scan or a filling disk.
	Warn func(scanner.Warning)
}

// ThumbnailKey formats the deterministic key for a photo's thumbnail file.
func ThumbnailKey(photoID, mtimeNS, size int64) string {
	return fmt.Sprintf("%d-%d-%d", photoID, mtimeNS, size)
}

// Run performs one full scan cycle: allocates a new scan_run row then delegates
// to RunWithScanID. The caller receives counts even on error.
func (idx *Indexer) Run(ctx context.Context, opts Options) (catalog.ScanCounts, error) {
	scanID, err := idx.Store.StartScan(ctx, idx.now())
	if err != nil {
		return catalog.ScanCounts{}, err
	}
	return idx.RunWithScanID(ctx, scanID, opts)
}

// RunWithScanID executes the scan against an already-reserved scan_run row.
// The admin HTTP handler uses this so it can reply 202 with the scan_id
// before background work starts.
func (idx *Indexer) RunWithScanID(ctx context.Context, scanID int64, opts Options) (counts catalog.ScanCounts, runErr error) {
	now := idx.now()
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
		idx.warn(w)
	})
	if walkErr != nil {
		return counts, walkErr
	}
	counts.Seen = int64(len(entries))
	opts.skipThumbnails = !idx.thumbnailsAllowed(&counts)

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
		return idx.handleNew(ctx, s, entry, albumID, opts, scanID, src, now, counts)
	}
	if entry.Size != existing.FileSize || entry.MTimeNS != existing.FileMTimeNS {
		return idx.handleChanged(ctx, s, entry, existing, opts, scanID, src, now, counts)
	}
	return idx.handleUnchanged(ctx, s, entry, existing, opts, scanID, src, now, counts)
}

func (idx *Indexer) handleNew(
	ctx context.Context,
	s *catalog.Store,
	entry *scanner.Entry,
	albumID int64,
	opts Options,
	scanID int64,
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
	if opts.skipThumbnails {
		return nil // catalogued without a thumbnail; the disk is too full
	}
	key := ThumbnailKey(id, entry.MTimeNS, entry.Size)
	if err := idx.Thumbnail(ctx, src, key, maxDim(meta.Width, meta.Height)); err != nil {
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
	opts Options,
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

	if opts.skipThumbnails {
		// Leave the existing key alone: the old thumbnail file is still on disk
		// and still serves. Only new generation stops.
		counts.Changed++
		return s.UpdatePhoto(ctx, existing, scanID, now)
	}
	newKey := ThumbnailKey(existing.ID, entry.MTimeNS, entry.Size)
	if err := idx.Thumbnail(ctx, src, newKey, maxDim(meta.Width, meta.Height)); err != nil {
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
	if opts.RebuildThumbnails && !opts.skipThumbnails {
		key := ThumbnailKey(existing.ID, entry.MTimeNS, entry.Size)
		if err := idx.Thumbnail(ctx, src, key, maxDim(existing.Width, existing.Height)); err != nil {
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

// warn forwards a warning to the optional sink. Counting happens at the call
// site, because walk warnings and indexer warnings are counted separately.
func (idx *Indexer) warn(w scanner.Warning) {
	if idx.Warn != nil {
		idx.Warn(w)
	}
}

// thumbnailsAllowed is consulted once per run: a syscall per photo would be
// wasted work, and a decision that flipped mid-run would be harder to explain.
func (idx *Indexer) thumbnailsAllowed(counts *catalog.ScanCounts) bool {
	if idx.MinFreeBytes <= 0 {
		return true
	}
	probe := idx.FreeSpace
	if probe == nil {
		probe = FreeBytes
	}
	free, err := probe(idx.ThumbnailDir)
	if err != nil {
		// Cannot tell. Keep generating — a broken statfs must not quietly turn
		// thumbnails off for good.
		return true
	}
	if free >= uint64(idx.MinFreeBytes) {
		return true
	}
	counts.Warnings++
	idx.warn(scanner.Warning{Path: idx.ThumbnailDir, Code: scanner.CodeLowDiskSpace})
	return false
}

func (idx *Indexer) now() time.Time {
	if idx.Now != nil {
		return idx.Now()
	}
	return time.Now().UTC()
}

func maxDim(w, h int) int {
	if w > h {
		return w
	}
	return h
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
