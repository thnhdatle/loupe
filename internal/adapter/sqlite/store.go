// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	// Registers the pure-Go "sqlite" database/sql driver (no cgo).
	_ "modernc.org/sqlite"

	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// driverName is the database/sql driver registered by modernc.org/sqlite.
const driverName = "sqlite"

// MaxReadPool caps the read-only connection pool (min(GOMAXPROCS, 8)).
const MaxReadPool = 8

// Synchronous modes accepted by WithSynchronous.
const (
	SynchronousOff    = "OFF"
	SynchronousNormal = "NORMAL"
	SynchronousFull   = "FULL"
	SynchronousExtra  = "EXTRA"
)

type config struct {
	clock               clock.Clock
	synchronous         string
	backupBeforeMigrate bool
	readPool            int
	contentMaxBytes     int
	migrations          fs.FS
}

// Option configures Open.
type Option func(*config)

// WithClock sets the clock used for ingest_stats hours and content insert
// times (default: the system clock).
func WithClock(c clock.Clock) Option { return func(o *config) { o.clock = c } }

// WithSynchronous sets PRAGMA synchronous on the writer (default NORMAL).
func WithSynchronous(mode string) Option {
	return func(o *config) { o.synchronous = strings.ToUpper(mode) }
}

// WithBackupBeforeMigrate enables or disables the VACUUM INTO backup taken
// before pending migrations run on an existing database (default enabled).
func WithBackupBeforeMigrate(on bool) Option {
	return func(o *config) { o.backupBeforeMigrate = on }
}

// WithReadPoolSize sets the size of the read-only pool (default
// min(GOMAXPROCS, MaxReadPool)).
func WithReadPoolSize(n int) Option { return func(o *config) { o.readPool = n } }

// WithContentMaxBytes sets content.max_bytes, the body budget of one span
// across all of its content parts: the writer caps the stored body bytes
// of each span across batches, whatever the arrival order of its span and
// log events.
// Zero or less means no limit (default).
func WithContentMaxBytes(n int) Option { return func(o *config) { o.contentMaxBytes = n } }

func withMigrationsFS(f fs.FS) Option { return func(o *config) { o.migrations = f } }

func defaultConfig() config {
	return config{
		clock:               clock.System{},
		synchronous:         SynchronousNormal,
		backupBeforeMigrate: true,
		readPool:            min(runtime.GOMAXPROCS(0), MaxReadPool),
		migrations:          embeddedMigrations(),
	}
}

func (c *config) validate() error {
	var errs []error
	switch c.synchronous {
	case SynchronousOff, SynchronousNormal, SynchronousFull, SynchronousExtra:
	default:
		errs = append(errs, fmt.Errorf("%w: synchronous %q", ErrInvalidOption, c.synchronous))
	}
	if c.readPool < 1 {
		errs = append(errs, fmt.Errorf("%w: read pool size %d", ErrInvalidOption, c.readPool))
	}
	if c.clock == nil {
		errs = append(errs, fmt.Errorf("%w: nil clock", ErrInvalidOption))
	}
	return errors.Join(errs...)
}

// Store is the SQLite storage adapter. It implements trace.SpanStore,
// query.TraceReader, query.SessionReader, query.StatsReader,
// rollup.RollupReader, retention.Store and admin.Backuper; Admin returns the
// admin.Store. Write methods must be called from one goroutine (the single
// writer); read methods are safe for concurrent use.
type Store struct {
	path  string
	cfg   config
	lock  *os.File
	w     *sql.DB // exactly one connection: the single writer
	r     *sql.DB // read-only pool (mode=ro, query_only)
	codec *codec
	stmts *writeStmts

	closeOnce sync.Once
	closeErr  error
}

// Open opens or creates the database at path: it takes the flock on
// <path>.lock, sets and verifies the pragmas, applies pending migrations
// (after a VACUUM INTO <path>.pre-v<N> backup when enabled and the schema
// version is above 0) and opens the read-only pool.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrInvalidOption)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: resolve path %s: %w", path, err)
	}
	s := &Store{path: abs, cfg: cfg}
	if err := s.open(ctx); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

func (s *Store) open(ctx context.Context) error {
	lock, err := acquireLock(s.path + ".lock")
	if err != nil {
		return err
	}
	s.lock = lock
	if s.w, err = openWriter(ctx, s.path, s.cfg.synchronous); err != nil {
		return err
	}
	if err = migrate(ctx, s.w, s.path, s.cfg); err != nil {
		return err
	}
	if s.r, err = openReaders(ctx, s.path, s.cfg.readPool); err != nil {
		return err
	}
	if s.codec, err = newCodec(s.cfg.readPool); err != nil {
		return err
	}
	if s.stmts, err = prepareWriteStmts(ctx, s.w); err != nil {
		return err
	}
	return nil
}

// Path returns the absolute path of the database file.
func (s *Store) Path() string { return s.path }

// Close closes the pools and releases the lock. It is idempotent.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		var errs []error
		if s.stmts != nil {
			errs = append(errs, s.stmts.close())
		}
		if s.r != nil {
			errs = append(errs, s.r.Close())
		}
		if s.w != nil {
			errs = append(errs, s.w.Close())
		}
		if s.codec != nil {
			s.codec.close()
		}
		if s.lock != nil {
			errs = append(errs, releaseLock(s.lock))
		}
		if err := errors.Join(errs...); err != nil {
			s.closeErr = fmt.Errorf("sqlite: close: %w", err)
		}
	})
	return s.closeErr
}
