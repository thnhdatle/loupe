// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
)

func WithMigrationsFS(f fs.FS) Option { return withMigrationsFS(f) }

func EmbeddedMigrations() fs.FS { return embeddedMigrations() }

var MigrationStatusOf = migrationStatus

func (s *Store) ReaderExec(ctx context.Context, q string) error {
	if _, err := s.r.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("reader exec: %w", err)
	}
	return nil
}

func (s *Store) ReadPool() *sql.DB { return s.r }
