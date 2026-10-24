// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const versionTable = "goose_db_version"

func embeddedMigrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(fmt.Sprintf("sqlite: embedded migrations: %v", err))
	}
	return sub
}

func newProvider(db *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	p, err := goose.NewProvider(goose.DialectSQLite3, db, fsys,
		goose.WithDisableGlobalRegistry(true), goose.WithTableName(versionTable))
	if err != nil {
		return nil, fmt.Errorf("sqlite: migrations: %w", err)
	}
	return p, nil
}

func BackupPath(dbPath string, n int64) string { return fmt.Sprintf("%s.pre-v%d", dbPath, n) }

func migrate(ctx context.Context, db *sql.DB, path string, cfg config) error {
	p, err := newProvider(db, cfg.migrations)
	if err != nil {
		return err
	}
	current, target, err := p.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: read schema version: %w", err)
	}
	if current > target {
		return fmt.Errorf("%w: database at v%d, binary at v%d", ErrDowngrade, current, target)
	}
	if current == target {
		return nil
	}
	if cfg.backupBeforeMigrate && current > 0 {
		if err := backupBeforeMigrate(ctx, db, path, target); err != nil {
			return err
		}
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("sqlite: migrate v%d -> v%d: %w", current, target, err)
	}
	return nil
}

func backupBeforeMigrate(ctx context.Context, db *sql.DB, path string, target int64) error {
	size, err := fileSize(path)
	if err != nil {
		return err
	}
	free, err := freeDiskBytes(filepath.Dir(path))
	if err != nil {
		return err
	}
	if free < size {
		return fmt.Errorf("%w: %d bytes free, database is %d bytes", ErrInsufficientDisk, free, size)
	}
	dst := BackupPath(path, target)
	if err := vacuumInto(ctx, db, dst); err != nil {
		return fmt.Errorf("sqlite: pre-migration backup: %w", err)
	}
	return nil
}

func vacuumInto(ctx context.Context, db *sql.DB, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrBackupExists, dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sqlite: stat %s: %w", dst, err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("sqlite: vacuum into %s: %w", dst, err)
	}
	return nil
}

func fileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite: stat %s: %w", path, err)
	}
	return st.Size(), nil
}

type MigrationStatus struct {
	Current int64
	Target  int64
	Pending []int64
}

func Migrations(ctx context.Context, path string) (MigrationStatus, error) {
	return migrationStatus(ctx, path, embeddedMigrations())
}

func migrationStatus(ctx context.Context, path string, fsys fs.FS) (MigrationStatus, error) {
	versions, err := sourceVersions(fsys)
	if err != nil {
		return MigrationStatus{}, err
	}
	st := MigrationStatus{}
	if len(versions) > 0 {
		st.Target = versions[len(versions)-1]
	}
	if _, err := os.Stat(path); err == nil {
		if st.Current, err = readVersion(ctx, path); err != nil {
			return MigrationStatus{}, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return MigrationStatus{}, fmt.Errorf("sqlite: stat %s: %w", path, err)
	}
	for _, v := range versions {
		if v > st.Current {
			st.Pending = append(st.Pending, v)
		}
	}
	return st, nil
}

func sourceVersions(fsys fs.FS) ([]int64, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list migrations: %w", err)
	}
	out := make([]int64, 0, len(names))
	for _, n := range names {
		v, err := goose.NumericComponent(n)
		if err != nil {
			return nil, fmt.Errorf("sqlite: migration %s: %w", n, err)
		}
		out = append(out, v)
	}
	slices.Sort(out)
	return out, nil
}

func readVersion(ctx context.Context, path string) (int64, error) {
	db, err := sql.Open(driverName, dsn(path, readOnly(), readerPragmas()))
	if err != nil {
		return 0, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	var n int
	err = db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", versionTable).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: read schema of %s: %w", path, err)
	}
	if n == 0 {
		return 0, nil
	}
	var v int64
	err = db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version_id), 0) FROM "+versionTable+" WHERE is_applied").Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("sqlite: read schema version of %s: %w", path, err)
	}
	return v, nil
}
