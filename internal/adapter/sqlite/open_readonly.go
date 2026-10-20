// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// OpenReadOnly opens the database at path for reading only. It takes no lock,
// never migrates and opens only the read pool (mode=ro, query_only), so it is
// safe while a server owns the database. It fails with ErrPendingMigrations
// when the schema is behind this binary and with ErrDowngrade when it is
// ahead. Write methods of the returned Store fail with an error.
func OpenReadOnly(ctx context.Context, path string, opts ...Option) (*Store, error) {
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
	st, err := migrationStatus(ctx, abs, cfg.migrations)
	if err != nil {
		return nil, err
	}
	if st.Current > st.Target {
		return nil, fmt.Errorf("%w: database at v%d, binary at v%d", ErrDowngrade, st.Current, st.Target)
	}
	if len(st.Pending) > 0 {
		return nil, fmt.Errorf("%w: database at v%d, binary at v%d", ErrPendingMigrations, st.Current, st.Target)
	}
	s := &Store{path: abs, cfg: cfg}
	if s.w, err = closedWriter(); err != nil {
		return nil, err
	}
	if s.r, err = openReaders(ctx, abs, cfg.readPool); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	if s.codec, err = newCodec(cfg.readPool); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// closedWriter returns a closed handle standing in for the writer of a
// read-only Store, so write paths return "database is closed" instead of
// dereferencing nil.
func closedWriter() (*sql.DB, error) {
	db, err := sql.Open(driverName, "file::memory:")
	if err != nil {
		return nil, fmt.Errorf("sqlite: read-only writer stub: %w", err)
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("sqlite: read-only writer stub: %w", err)
	}
	return db, nil
}
