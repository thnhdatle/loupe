// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
)

// newDB creates a migrated empty database and returns a config file for it.
func newDB(t *testing.T) (cfgPath, dbPath string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "loupe.db")
	st, err := sqlite.Open(context.Background(), dbPath, sqlite.WithBackupBeforeMigrate(false))
	require.NoError(t, err)
	require.NoError(t, st.Close())
	return writeCfgYAML(t, "storage:\n  path: \""+dbPath+"\"\n"), dbPath
}

func TestDBStats(t *testing.T) {
	t.Parallel()
	cfg, dbPath := newDB(t)
	out, err := runCmd(t, "--config", cfg, "db", "stats")
	require.NoError(t, err)
	assert.Contains(t, out, "rows.spans: 0")
	assert.Contains(t, out, "schema_version: 1")
	assert.Contains(t, out, "oldest_span: -")

	out, err = runCmd(t, "--config", cfg, "db", "stats", "--json")
	require.NoError(t, err)
	var st sqlite.DBStats
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.Equal(t, dbPath, st.Path)
	assert.Positive(t, st.PageCount)
	assert.Len(t, st.Tables, 8)
}

func TestDBStats_Online(t *testing.T) {
	t.Parallel()
	cfg, dbPath := newDB(t)
	st, err := sqlite.Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	_, err = runCmd(t, "--config", cfg, "db", "stats")
	require.NoError(t, err)
}

func TestDBStats_MissingFile(t *testing.T) {
	t.Parallel()
	cfg := writeCfgYAML(t, "storage:\n  path: \""+filepath.Join(t.TempDir(), "none.db")+"\"\n")
	_, err := runCmd(t, "--config", cfg, "db", "stats")
	require.Error(t, err)
}

func TestDBBackup(t *testing.T) {
	t.Parallel()
	cfg, dbPath := newDB(t)
	dst := filepath.Join(t.TempDir(), "copy.db")
	st, err := sqlite.Open(context.Background(), dbPath) // online: the server holds the lock
	require.NoError(t, err)
	defer func() { _ = st.Close() }()

	_, err = runCmd(t, "--config", cfg, "db", "backup", dst)
	require.NoError(t, err)
	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Positive(t, info.Size())

	_, err = runCmd(t, "--config", cfg, "db", "backup", dst)
	require.ErrorIs(t, err, sqlite.ErrBackupExists)
}

func TestDBVacuum_Offline(t *testing.T) {
	t.Parallel()
	cfg, _ := newDB(t)
	out, err := runCmd(t, "--config", cfg, "db", "vacuum", "--offline")
	require.NoError(t, err)
	assert.Contains(t, out, "vacuum done")
	out, err = runCmd(t, "--config", cfg, "db", "vacuum", "--offline", "--full")
	require.NoError(t, err)
	assert.Contains(t, out, "full vacuum done")
}

func TestOffline_FailsWhenLockHeld(t *testing.T) {
	t.Parallel()
	cfg, dbPath := newDB(t)
	st, err := sqlite.Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer func() { _ = st.Close() }()
	for _, args := range [][]string{
		{"db", "vacuum", "--offline"},
		{"db", "vacuum", "--offline", "--full"},
		{"pricing", "recompute", "--offline", "--since", "2026-01-01"},
	} {
		_, err := runCmd(t, append([]string{"--config", cfg}, args...)...)
		require.ErrorIs(t, err, sqlite.ErrLocked, args)
	}
}

func TestPricingRecompute_Offline(t *testing.T) {
	t.Parallel()
	cfg, _ := newDB(t)
	out, err := runCmd(t, "--config", cfg, "pricing", "recompute", "--offline", "--since", "2026-01-01")
	require.NoError(t, err)
	assert.Contains(t, out, "recompute done")
}
