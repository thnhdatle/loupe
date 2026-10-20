// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
)

func TestOpen_PersistentPragmasSet(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	openAt(t, path)
	db := rawDB(t, path)
	var mode string
	require.NoError(t, db.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "wal", mode)
	assert.Equal(t, int64(2), queryInt(t, db, "PRAGMA auto_vacuum"), "auto_vacuum must be INCREMENTAL")
	assert.Equal(t, int64(1), queryInt(t, db, "SELECT count(*) FROM meta WHERE key = 'schema'"))
}

func TestOpen_SecondOpenIsLocked(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	s := openAt(t, path)
	_, err := sqlite.Open(context.Background(), path)
	require.ErrorIs(t, err, sqlite.ErrLocked)

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "Close is idempotent")
	again := openAt(t, path)
	assert.Equal(t, path, again.Path())
}

func TestOpen_RejectsNonIncrementalDatabase(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), "CREATE TABLE foreign_data (x)")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = sqlite.Open(context.Background(), path)
	require.ErrorIs(t, err, sqlite.ErrPragma)
	// A failed Open releases the lock.
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, lock.Close())
}

func TestOpen_InvalidOptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name string
		path string
		opts []sqlite.Option
	}{
		{name: "empty path", path: ""},
		{name: "synchronous", path: dbPath(t), opts: []sqlite.Option{sqlite.WithSynchronous("sometimes")}},
		{name: "pool", path: dbPath(t), opts: []sqlite.Option{sqlite.WithReadPoolSize(0)}},
		{name: "clock", path: dbPath(t), opts: []sqlite.Option{sqlite.WithClock(nil)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := sqlite.Open(ctx, tc.path, tc.opts...)
			require.ErrorIs(t, err, sqlite.ErrInvalidOption)
		})
	}
}

func TestOpen_SynchronousFromConfig(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"full", sqlite.SynchronousExtra, sqlite.SynchronousOff} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			s := openStore(t, sqlite.WithSynchronous(mode))
			require.NotNil(t, s)
		})
	}
}

func TestOpen_MissingDirectoryFails(t *testing.T) {
	t.Parallel()
	_, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "missing", "loupe.db"))
	require.Error(t, err)
}

func TestOpen_ReadersAreReadOnly(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	// The read pool backs every reader; a write through it must be refused.
	err := s.ReaderExec(context.Background(), "CREATE TABLE x (y)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "readonly")
}
