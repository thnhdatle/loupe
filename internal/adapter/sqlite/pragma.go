// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Pragma values of the store (WAL, durability, vacuum and checkpoint policy).
const (
	busyTimeoutMs     = 5000
	journalSizeLimit  = 64 << 20 // 67108864
	writerCacheSizeKB = -65536   // negative: KiB, i.e. 64 MiB
	walAutocheckpoint = 1000     // pages
)

// pragma is one per-connection setting: the value set through the DSN and
// the value PRAGMA name reports back.
type pragma struct {
	name string
	set  string
	want string
}

func commonPragmas() []pragma {
	return []pragma{
		{name: "busy_timeout", set: strconv.Itoa(busyTimeoutMs), want: strconv.Itoa(busyTimeoutMs)},
		{name: "foreign_keys", set: "1", want: "1"},
		{name: "temp_store", set: "MEMORY", want: "2"},
		{name: "secure_delete", set: "FAST", want: "2"},
	}
}

var synchronousCodes = map[string]string{
	SynchronousOff: "0", SynchronousNormal: "1", SynchronousFull: "2", SynchronousExtra: "3",
}

func writerPragmas(synchronous string) []pragma {
	return append(commonPragmas(),
		pragma{name: "synchronous", set: synchronous, want: synchronousCodes[synchronous]},
		pragma{name: "journal_size_limit", set: strconv.Itoa(journalSizeLimit), want: strconv.Itoa(journalSizeLimit)},
		pragma{name: "cache_size", set: strconv.Itoa(writerCacheSizeKB), want: strconv.Itoa(writerCacheSizeKB)},
		pragma{name: "wal_autocheckpoint", set: strconv.Itoa(walAutocheckpoint), want: strconv.Itoa(walAutocheckpoint)},
	)
}

func readerPragmas() []pragma {
	return append(commonPragmas(), pragma{name: "query_only", set: "1", want: "1"})
}

// readOnly returns the URI parameters of a read-only connection.
func readOnly() url.Values { return url.Values{"mode": {"ro"}} }

// dsn builds a file: URI for path with SQLite URI parameters and the driver's
// _pragma and _txlock parameters.
func dsn(path string, params url.Values, pragmas []pragma) string {
	q := url.Values{}
	for k, v := range params {
		q[k] = v
	}
	for _, p := range pragmas {
		q.Add("_pragma", p.name+"("+p.set+")")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	return u.String()
}

// openWriter opens the single writer connection, makes a new database
// auto_vacuum=INCREMENTAL before any table exists, switches it to WAL and
// verifies every writer pragma.
func openWriter(ctx context.Context, path, synchronous string) (*sql.DB, error) {
	pragmas := writerPragmas(synchronous)
	db, err := sql.Open(driverName, dsn(path, url.Values{"_txlock": {"immediate"}}, pragmas))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open writer: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if err := initWriter(ctx, db, pragmas); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func initWriter(ctx context.Context, db *sql.DB, pragmas []pragma) error {
	// auto_vacuum takes effect only before the first table is created; on an
	// existing database this statement is a no-op and verification decides.
	if _, err := db.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return fmt.Errorf("sqlite: set auto_vacuum: %w", err)
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return fmt.Errorf("sqlite: set journal_mode: %w", err)
	}
	all := append([]pragma{
		{name: "journal_mode", want: "wal"},
		{name: "auto_vacuum", want: "2"},
	}, pragmas...)
	return verifyPragmas(ctx, db, all)
}

// openReaders opens the read-only pool (mode=ro, query_only=ON) and verifies its pragmas.
func openReaders(ctx context.Context, path string, size int) (*sql.DB, error) {
	pragmas := readerPragmas()
	db, err := sql.Open(driverName, dsn(path, readOnly(), pragmas))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open readers: %w", err)
	}
	db.SetMaxOpenConns(size)
	db.SetMaxIdleConns(size)
	all := append([]pragma{{name: "journal_mode", want: "wal"}}, pragmas...)
	if err := verifyPragmas(ctx, db, all); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

// verifyPragmas checks every pragma on one connection of db; all
// connections of a pool are opened from the same DSN.
func verifyPragmas(ctx context.Context, db *sql.DB, pragmas []pragma) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: connect: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var errs []error
	for _, p := range pragmas {
		var got string
		if err := conn.QueryRowContext(ctx, "PRAGMA "+p.name).Scan(&got); err != nil {
			return fmt.Errorf("sqlite: read pragma %s: %w", p.name, err)
		}
		if !strings.EqualFold(got, p.want) {
			errs = append(errs, fmt.Errorf("%w: %s = %s, want %s", ErrPragma, p.name, got, p.want))
		}
	}
	return errors.Join(errs...)
}
