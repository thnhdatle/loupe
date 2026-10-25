// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func latestVersion(t *testing.T) int64 {
	t.Helper()
	files, err := fs.Glob(sqlite.EmbeddedMigrations(), "*.sql")
	require.NoError(t, err)
	return int64(len(files))
}

func nextMigrations(t *testing.T) fs.FS {
	t.Helper()
	files, err := fs.Glob(sqlite.EmbeddedMigrations(), "*.sql")
	require.NoError(t, err)
	out := fstest.MapFS{}
	for _, name := range files {
		data, err := fs.ReadFile(sqlite.EmbeddedMigrations(), name)
		require.NoError(t, err)
		out[name] = &fstest.MapFile{Data: data}
	}
	out[fmt.Sprintf("%05d_extra.sql", len(files)+1)] = &fstest.MapFile{Data: []byte(
		"-- +goose Up\nCREATE TABLE extra (x INTEGER);\n-- +goose Down\nDROP TABLE extra;\n")}
	return out
}

func allVersions(n int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i) + 1
	}
	return out
}

func TestMigrations_StatusOfFreshAndMigratedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := dbPath(t)
	st, err := sqlite.Migrations(ctx, path)
	require.NoError(t, err)
	latest := latestVersion(t)
	assert.Equal(t, sqlite.MigrationStatus{Current: 0, Target: latest, Pending: allVersions(latest)}, st)

	s := openAt(t, path)
	st, err = sqlite.Migrations(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, sqlite.MigrationStatus{Current: latest, Target: latest}, st, "dry-run works while the store is open")

	st, err = sqlite.MigrationStatusOf(ctx, path, nextMigrations(t))
	require.NoError(t, err)
	assert.Equal(t, []int64{latest + 1}, st.Pending)
	require.NoError(t, s.Close())
	_, err = os.Stat(sqlite.BackupPath(path, latest))
	assert.ErrorIs(t, err, os.ErrNotExist, "no backup for a fresh database")
}

func TestMigrations_BackupBeforeMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := dbPath(t)
	s := openAt(t, path)
	writeSpans(t, s, mkSpan(1, 1, 0, trace.KindLLM))
	require.NoError(t, s.Close())

	s = openAt(t, path, sqlite.WithMigrationsFS(nextMigrations(t)))
	db := rawDB(t, path)
	assert.Equal(t, int64(1), queryInt(t, db, "SELECT count(*) FROM sqlite_master WHERE name = 'extra'"))
	backup := rawDB(t, sqlite.BackupPath(path, latestVersion(t)+1))
	assert.Equal(t, int64(1), queryInt(t, backup, "SELECT count(*) FROM spans"))
	assert.Equal(t, int64(0), queryInt(t, backup, "SELECT count(*) FROM sqlite_master WHERE name = 'extra'"),
		"the backup holds the schema before the migration")
	require.NoError(t, s.Close())

	_, err := sqlite.Open(ctx, path)
	require.ErrorIs(t, err, sqlite.ErrDowngrade)
}

func TestMigrations_BackupDisabledOrBlocked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		backup  bool
		occupy  bool
		wantErr error
	}{
		{name: "disabled", backup: false},
		{name: "target exists", backup: true, occupy: true, wantErr: sqlite.ErrBackupExists},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := dbPath(t)
			s := openAt(t, path)
			require.NoError(t, s.Close())
			if tc.occupy {
				require.NoError(t, os.WriteFile(sqlite.BackupPath(path, latestVersion(t)+1), []byte("x"), 0o600))
			}
			s, err := sqlite.Open(context.Background(), path, sqlite.WithMigrationsFS(nextMigrations(t)),
				sqlite.WithBackupBeforeMigrate(tc.backup))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				st, serr := sqlite.MigrationStatusOf(context.Background(), path, nextMigrations(t))
				require.NoError(t, serr)
				assert.Equal(t, latestVersion(t), st.Current, "nothing was migrated")
				return
			}
			require.NoError(t, err)
			require.NoError(t, s.Close())
			_, err = os.Stat(sqlite.BackupPath(path, latestVersion(t)+1))
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestMigrations_BadSource(t *testing.T) {
	t.Parallel()
	_, err := sqlite.MigrationStatusOf(context.Background(), dbPath(t), fstest.MapFS{"x_bad.sql": {Data: []byte("")}})
	require.Error(t, err)
	_, err = sqlite.Open(context.Background(), dbPath(t), sqlite.WithMigrationsFS(fstest.MapFS{
		"00001_broken.sql": {Data: []byte("-- +goose Up\nCREATE TABLE (;\n")},
	}))
	require.Error(t, err)
}

func TestMigrations_AddStatusMessageToExistingDatabase(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	initial, err := fs.ReadFile(sqlite.EmbeddedMigrations(), "00001_initial.sql")
	require.NoError(t, err)
	v1, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	ctx := context.Background()
	_, err = v1.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL")
	require.NoError(t, err)
	_, err = v1.ExecContext(ctx, "PRAGMA journal_mode = WAL")
	require.NoError(t, err)
	require.NoError(t, sqlite.ApplyMigrations(ctx, v1,
		fstest.MapFS{"00001_initial.sql": {Data: initial}}))
	require.NoError(t, v1.Close())

	s := openAt(t, path)
	require.NoError(t, s.Close())
	db := rawDB(t, path)
	var def string
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT dflt_value FROM pragma_table_info('spans') WHERE name = 'status_message'").Scan(&def))
	assert.Equal(t, "''", def)
	_, err = os.Stat(sqlite.BackupPath(path, latestVersion(t)))
	assert.NoError(t, err, "the v1 database is backed up before the migration")
}
