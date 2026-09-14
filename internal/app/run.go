package app

import (
	"context"
	"database/sql"
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
	"photo-browser/internal/users"
)

const usage = `usage:
  photo-app index [--rebuild-thumbnails]
  photo-app rebuild
  photo-app admin <sub-command>
  photo-app serve
  photo-app healthcheck [--url=<url>]
  photo-app backup --out=<path>`

// Commands isolates side-effectful operations so tests can substitute fakes.
type Commands struct {
	Index     func(ctx context.Context, opts indexer.Options) error
	Rebuild   func(ctx context.Context) error
	Admin     func(ctx context.Context, args []string, stdout, stderr io.Writer) int
	Serve     func(ctx context.Context, stdout, stderr io.Writer) error
	Lock      func() (io.Closer, error)
	ServeLock func() (io.Closer, error) // separate from Lock so serve can coexist with cron `index`

	Healthcheck func(ctx context.Context, args []string, stdout, stderr io.Writer) int
	Backup      func(ctx context.Context, args []string, stdout, stderr io.Writer) int
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
	case "admin":
		if cmds.Admin == nil {
			fmt.Fprintln(stderr, "admin not wired")
			return 1
		}
		// admin shares the same index.lock so it can never race with a
		// concurrent index/rebuild writing to the same SQLite file.
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
		return cmds.Admin(ctx, args[1:], stdout, stderr)
	case "serve":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "serve takes no arguments\n%s\n", usage)
			return 2
		}
		if cmds.Serve == nil || cmds.ServeLock == nil {
			fmt.Fprintln(stderr, "serve not wired")
			return 1
		}
		// serve.lock is separate from index.lock so a long-lived serve process
		// doesn't block cron-scheduled `photo-app index` runs.
		lock, err := cmds.ServeLock()
		if err != nil {
			if errors.Is(err, filelock.ErrAlreadyLocked) {
				fmt.Fprintln(stderr, "photo-app serve is already running")
				return 3
			}
			fmt.Fprintf(stderr, "lock: %v\n", err)
			return 1
		}
		defer lock.Close()
		if err := cmds.Serve(ctx, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "photo-app: %v\n", err)
			return 1
		}
		return 0
	case "healthcheck":
		if cmds.Healthcheck == nil {
			fmt.Fprintln(stderr, "healthcheck not wired")
			return 1
		}
		// No lock: this runs while serve holds serve.lock, and it only reads
		// over HTTP.
		return cmds.Healthcheck(ctx, args[1:], stdout, stderr)
	case "backup":
		if cmds.Backup == nil {
			fmt.Fprintln(stderr, "backup not wired")
			return 1
		}
		// Shares index.lock so a snapshot can never race a concurrent index
		// or admin write.
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
		return cmds.Backup(ctx, args[1:], stdout, stderr)
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
		ServeLock: func() (io.Closer, error) {
			if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
				return nil, err
			}
			return filelock.Acquire(filepath.Join(cfg.DataDir, "serve.lock"))
		},
		Serve: func(ctx context.Context, stdout, stderr io.Writer) error {
			return serveFromEnv(ctx, env, stdout, stderr)
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
		Healthcheck: func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
			url := healthcheckURL(cfg.HTTPListen)
			for _, a := range args {
				if !strings.HasPrefix(a, "--url=") {
					fmt.Fprintf(stderr, "unknown flag: %s\n%s\n", a, usage)
					return 2
				}
				url = strings.TrimPrefix(a, "--url=")
			}
			return RunHealthcheck(ctx, url, nil, stderr)
		},
		Backup: func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
			out := ""
			for _, a := range args {
				if !strings.HasPrefix(a, "--out=") {
					fmt.Fprintf(stderr, "unknown flag: %s\n%s\n", a, usage)
					return 2
				}
				out = strings.TrimPrefix(a, "--out=")
			}
			db, err := env.database()
			if err != nil {
				fmt.Fprintf(stderr, "backup: %v\n", err)
				return 1
			}
			if err := RunBackup(ctx, db, out, stdout); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return 1
			}
			return 0
		},
		Admin: func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
			us, err := env.usersStore()
			if err != nil {
				fmt.Fprintf(stderr, "admin: %v\n", err)
				return 1
			}
			return RunAdmin(ctx, AdminEnv{Users: us}, args, stdout, stderr)
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
	users *users.Store
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

// database returns the shared *sql.DB, opening it if needed. Callers must not
// close it — prodEnv.close owns the handle.
func (env *prodEnv) database() (*sql.DB, error) {
	if _, err := env.indexer(); err != nil {
		return nil, err
	}
	return env.store.RawDB(), nil
}

// usersStore lazily opens the DB and returns a users.Store on the shared handle.
func (env *prodEnv) usersStore() (*users.Store, error) {
	if env.users != nil {
		return env.users, nil
	}
	if _, err := env.indexer(); err != nil {
		return nil, err
	}
	env.users = users.NewStore(env.store.RawDB())
	return env.users, nil
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
