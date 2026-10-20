// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// statsTables are the tables whose rows DBStats counts, in report order.
var statsTables = []string{
	"spans", "traces", "sessions", "span_content",
	"rollup_hour", "rollup_day", "rollup_user_hour", "rollup_user_day",
}

// TableCount is the row count of one table.
type TableCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// DBStats describes a database file for `loupe db stats`.
type DBStats struct {
	Path          string       `json:"path"`
	MainBytes     int64        `json:"main_bytes"`
	WALBytes      int64        `json:"wal_bytes"`
	SHMBytes      int64        `json:"shm_bytes"`
	PageSize      int64        `json:"page_size"`
	PageCount     int64        `json:"page_count"`
	FreelistCount int64        `json:"freelist_count"`
	SchemaVersion int64        `json:"schema_version"`
	Tables        []TableCount `json:"tables"`
	OldestSpan    *time.Time   `json:"oldest_span,omitempty"`
	NewestSpan    *time.Time   `json:"newest_span,omitempty"`
}

// ReadStats reports sizes, row counts and the span time range of the database
// at path. It opens the file read-only without taking the lock, so it is safe
// while a server runs.
func ReadStats(ctx context.Context, path string) (DBStats, error) {
	st := DBStats{Path: path}
	if _, err := os.Stat(path); err != nil {
		return st, fmt.Errorf("sqlite: stat %s: %w", path, err)
	}
	var err error
	if st.MainBytes, err = fileSize(path); err != nil {
		return st, err
	}
	if st.WALBytes, err = fileSize(path + "-wal"); err != nil {
		return st, err
	}
	if st.SHMBytes, err = fileSize(path + "-shm"); err != nil {
		return st, err
	}
	if st.SchemaVersion, err = readVersion(ctx, path); err != nil {
		return st, err
	}
	db, err := sql.Open(driverName, dsn(path, readOnly(), readerPragmas()))
	if err != nil {
		return st, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	for _, p := range []struct {
		pragma string
		dst    *int64
	}{{"page_size", &st.PageSize}, {"page_count", &st.PageCount}, {"freelist_count", &st.FreelistCount}} {
		if err := db.QueryRowContext(ctx, "PRAGMA "+p.pragma).Scan(p.dst); err != nil {
			return st, fmt.Errorf("sqlite: read %s: %w", p.pragma, err)
		}
	}
	if st.SchemaVersion == 0 {
		return st, nil
	}
	for _, t := range statsTables {
		var n int64
		q := "SELECT count(*) FROM " + t
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return st, fmt.Errorf("sqlite: count %s: %w", t, err)
		}
		st.Tables = append(st.Tables, TableCount{Table: t, Rows: n})
	}
	var lo, hi sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT min(start_ns), max(start_ns) FROM spans").Scan(&lo, &hi); err != nil {
		return st, fmt.Errorf("sqlite: read span range: %w", err)
	}
	if lo.Valid {
		a, b := time.Unix(0, lo.Int64).UTC(), time.Unix(0, hi.Int64).UTC()
		st.OldestSpan, st.NewestSpan = &a, &b
	}
	return st, nil
}

// BackupFile writes a consistent copy of the database at path to dst with
// VACUUM INTO on a read-only connection and without taking the lock, so it is
// safe while a server runs. It fails with ErrBackupExists when dst exists.
func BackupFile(ctx context.Context, path, dst string) error {
	if path == "" || dst == "" {
		return fmt.Errorf("%w: empty path", ErrInvalidOption)
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("sqlite: database %s: %w", path, err)
		}
		return fmt.Errorf("sqlite: stat %s: %w", path, err)
	}
	db, err := sql.Open(driverName, dsn(path, readOnly(), commonPragmas()))
	if err != nil {
		return fmt.Errorf("sqlite: open backup connection: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	return vacuumInto(ctx, db, dst)
}

// FullVacuum rewrites the whole database file with VACUUM and truncates the
// WAL. It runs on the writer connection, so call it only while no writer job
// runs (offline maintenance).
func (s *Store) FullVacuum(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("sqlite: vacuum: %w", err)
	}
	if _, err := s.Checkpoint(ctx); err != nil {
		return err
	}
	return nil
}
