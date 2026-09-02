package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"photo-browser/internal/catalog"
	"photo-browser/internal/config"
	"photo-browser/internal/database"
	"photo-browser/internal/filelock"
	"photo-browser/internal/indexer"
	"photo-browser/internal/media"
	"photo-browser/internal/scanner"
)

const usage = `usage:
  photo-app index [--rebuild-thumbnails]
  photo-app rebuild`

// Commands isolates side-effectful operations so tests can substitute fakes.
type Commands struct {
	Index   func(ctx context.Context, opts indexer.Options) error
	Rebuild func(ctx context.Context) error
	Lock    func() (io.Closer, error)
}

// Run dispatches args to Commands. Returns the process exit code:
// 0 success, 1 runtime failure, 2 usage error, 3 lock busy.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, cmds Commands) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "index":
		opts := indexer.Options{}
		for _, a := range args[1:] {
			switch a {
			case "--rebuild-thumbnails":
				opts.RebuildThumbnails = true
			default:
				fmt.Fprintf(stderr, "unknown flag: %s\n%s\n", a, usage)
				return 2
			}
		}
		return runLocked(ctx, stderr, cmds, func(ctx context.Context) error {
			return cmds.Index(ctx, opts)
		})
	case "rebuild":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "rebuild takes no arguments\n%s\n", usage)
			return 2
		}
		return runLocked(ctx, stderr, cmds, cmds.Rebuild)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n%s\n", args[0], usage)
		return 2
	}
}

func runLocked(ctx context.Context, stderr io.Writer, cmds Commands, fn func(context.Context) error) int {
	lock, err := cmds.Lock()
	if err != nil {
		if errors.Is(err, filelock.ErrAlreadyLocked) {
			fmt.Fprintln(stderr, "photo-app is already running")
			return 3
		}
		fmt.Fprintf(stderr, "lock: %v\n", err)
		return 1
	}
	defer lock.Close()
	if err := fn(ctx); err != nil {
		fmt.Fprintf(stderr, "photo-app: %v\n", err)
		return 1
	}
	return 0
}

// Main wires production dependencies and delegates to Run. Wiring is deferred
// into closures so unknown / usage-error commands never touch the filesystem.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "config: %v\n", err)
		return 2
	}
	env := &prodEnv{cfg: cfg, stdout: stdout}
	defer env.close()

	cmds := Commands{
		Lock: func() (io.Closer, error) {
			if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
				return nil, err
			}
			return filelock.Acquire(filepath.Join(cfg.DataDir, "index.lock"))
		},
		Index: func(ctx context.Context, opts indexer.Options) error {
			idx, err := env.indexer()
			if err != nil {
				return err
			}
			counts, err := idx.Run(ctx, opts)
			fmt.Fprintf(stdout, "scan: seen=%d new=%d changed=%d unchanged=%d removed=%d warnings=%d\n",
				counts.Seen, counts.New, counts.Changed, counts.Unchanged, counts.Removed, counts.Warnings)
			return err
		},
		Rebuild: func(ctx context.Context) error {
			idx, err := env.indexer()
			if err != nil {
				return err
			}
			if err := env.store.InTx(ctx, func(s *catalog.Store) error { return s.ClearCatalogue(ctx) }); err != nil {
				return fmt.Errorf("clear catalogue: %w", err)
			}
			if err := purgeThumbnails(cfg.ThumbnailDir); err != nil {
				return fmt.Errorf("purge thumbnails: %w", err)
			}
			counts, err := idx.Run(ctx, indexer.Options{})
			fmt.Fprintf(stdout, "rebuild: seen=%d new=%d warnings=%d\n",
				counts.Seen, counts.New, counts.Warnings)
			return err
		},
	}
	return Run(ctx, args, stdout, stderr, cmds)
}

// prodEnv lazily builds the production DB/store/indexer once per process.
type prodEnv struct {
	cfg    config.Config
	stdout io.Writer

	db    *databaseHandle
	store *catalog.Store
	idx   *indexer.Indexer
}

// databaseHandle wraps *sql.DB so we can close it even when store hides it.
type databaseHandle struct{ closer io.Closer }

func (env *prodEnv) indexer() (*indexer.Indexer, error) {
	if env.idx != nil {
		return env.idx, nil
	}
	for _, dir := range []string{env.cfg.DataDir, env.cfg.ThumbnailDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	db, err := database.Open(filepath.Join(env.cfg.DataDir, "photo.db"))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	env.db = &databaseHandle{closer: db}
	env.store = catalog.NewStore(db)
	thumb := media.NewThumbnailer(env.cfg.ThumbnailDir)
	env.idx = &indexer.Indexer{
		Store:        env.store,
		PhotoRoot:    env.cfg.PhotoRoot,
		ThumbnailDir: env.cfg.ThumbnailDir,
		Walk:         scanner.Walk,
		ReadMetadata: media.ReadMetadata,
		Thumbnail:    thumb.Generate,
	}
	return env.idx, nil
}

func (env *prodEnv) close() {
	if env.db != nil {
		_ = env.db.closer.Close()
	}
}

func purgeThumbnails(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".webp") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
