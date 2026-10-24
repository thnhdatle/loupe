// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func v2Migrations(t *testing.T) fs.FS {
	t.Helper()
	initial, err := fs.ReadFile(sqlite.EmbeddedMigrations(), "00001_initial.sql")
	require.NoError(t, err)
	return fstest.MapFS{
		"00001_initial.sql": {Data: initial},
		"00002_extra.sql": {Data: []byte("-- +goose Up\nCREATE TABLE extra (x INTEGER);\n" +
			"-- +goose Down\nDROP TABLE extra;\n")},
	}
}

func TestMigrations_StatusOfFreshAndMigratedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := dbPath(t)
	st, err := sqlite.Migrations(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, sqlite.MigrationStatus{Current: 0, Target: 1, Pending: []int64{1}}, st)

	s := openAt(t, path)
	st, err = sqlite.Migrations(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, sqlite.MigrationStatus{Current: 1, Target: 1}, st, "dry-run works while the store is open")

	st, err = sqlite.MigrationStatusOf(ctx, path, v2Migrations(t))
	require.NoError(t, err)
	assert.Equal(t, []int64{2}, st.Pending)
	require.NoError(t, s.Close())
	_, err = os.Stat(sqlite.BackupPath(path, 1))
	assert.ErrorIs(t, err, os.ErrNotExist, "no backup for a fresh database")
}

func TestMigrations_BackupBeforeMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := dbPath(t)
	s := openAt(t, path)
	writeSpans(t, s, mkSpan(1, 1, 0, trace.KindLLM))
	require.NoError(t, s.Close())

	s = openAt(t, path, sqlite.WithMigrationsFS(v2Migrations(t)))
	db := rawDB(t, path)
	assert.Equal(t, int64(1), queryInt(t, db, "SELECT count(*) FROM sqlite_master WHERE name = 'extra'"))
	backup := rawDB(t, sqlite.BackupPath(path, 2))
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
				require.NoError(t, os.WriteFile(sqlite.BackupPath(path, 2), []byte("x"), 0o600))
			}
			s, err := sqlite.Open(context.Background(), path, sqlite.WithMigrationsFS(v2Migrations(t)),
				sqlite.WithBackupBeforeMigrate(tc.backup))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				st, serr := sqlite.MigrationStatusOf(context.Background(), path, v2Migrations(t))
				require.NoError(t, serr)
				assert.Equal(t, int64(1), st.Current, "nothing was migrated")
				return
			}
			require.NoError(t, err)
			require.NoError(t, s.Close())
			_, err = os.Stat(sqlite.BackupPath(path, 2))
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
