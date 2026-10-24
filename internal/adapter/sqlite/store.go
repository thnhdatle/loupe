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

	_ "modernc.org/sqlite"

	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const driverName = "sqlite"

const MaxReadPool = 8

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

type Option func(*config)

func WithClock(c clock.Clock) Option { return func(o *config) { o.clock = c } }

func WithSynchronous(mode string) Option {
	return func(o *config) { o.synchronous = strings.ToUpper(mode) }
}

func WithBackupBeforeMigrate(on bool) Option {
	return func(o *config) { o.backupBeforeMigrate = on }
}

func WithReadPoolSize(n int) Option { return func(o *config) { o.readPool = n } }

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

// Store is the SQLite storage adapter.
type Store struct {
	path  string
	cfg   config
	lock  *os.File
	w     *sql.DB
	r     *sql.DB
	codec *codec
	stmts *writeStmts

	closeOnce sync.Once
	closeErr  error
}

// Open opens or creates the database at path and applies pending migrations.
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

func (s *Store) Path() string { return s.path }

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
