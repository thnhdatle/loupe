// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
)

// WithMigrationsFS replaces the embedded migrations (tests of upgrades).
func WithMigrationsFS(f fs.FS) Option { return withMigrationsFS(f) }

// EmbeddedMigrations exposes the embedded migration files.
func EmbeddedMigrations() fs.FS { return embeddedMigrations() }

// MigrationStatusOf is Migrations with an explicit migrations FS.
var MigrationStatusOf = migrationStatus

// ReaderExec runs a statement on the read-only pool.
func (s *Store) ReaderExec(ctx context.Context, q string) error {
	if _, err := s.r.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("reader exec: %w", err)
	}
	return nil
}

// ReadPool exposes the read-only pool for test assertions.
func (s *Store) ReadPool() *sql.DB { return s.r }
