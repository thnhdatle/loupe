// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/config"
)

func TestReadTokens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	require.NoError(t, os.WriteFile(good, []byte("a\n\n  b  \n"), 0o600))
	empty := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(empty, []byte("\n \n"), 0o600))

	got, err := readTokens(good)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, got)

	got, err = readTokens("")
	require.NoError(t, err)
	require.Empty(t, got)

	_, err = readTokens(empty)
	require.Error(t, err)
	_, err = readTokens(filepath.Join(dir, "missing"))
	require.Error(t, err)
}

func TestEnsureAPIToken(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.API.TokenFile = filepath.Join(t.TempDir(), "api_token")

	created, err := ensureAPIToken(&cfg)
	require.NoError(t, err)
	require.False(t, created, "generate_token is off")

	cfg.API.GenerateToken = true
	created, err = ensureAPIToken(&cfg)
	require.NoError(t, err)
	require.True(t, created)
	info, err := os.Stat(cfg.API.TokenFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	tokens, err := readTokens(cfg.API.TokenFile)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	require.Len(t, tokens[0], 64)

	created, err = ensureAPIToken(&cfg)
	require.NoError(t, err)
	require.False(t, created, "an existing file is kept")
}

func TestIsLoopback(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]bool{
		"127.0.0.1:1": true, "[::1]:1": true, "localhost:1": true,
		"0.0.0.0:1": false, "10.0.0.1:1": false, "example.com:1": false, "bad": false,
	} {
		require.Equal(t, want, isLoopback(addr), addr)
	}
}

func TestDBMigrate_DryRunThenApply(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "loupe.yaml")
	dbPath := filepath.Join(t.TempDir(), "loupe.db")
	require.NoError(t, os.WriteFile(cfgPath, []byte("storage:\n  path: "+dbPath+"\n"), 0o600))

	out, _, err := runRoot(t, "--config", cfgPath, "db", "migrate", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "current version: 0")
	require.Contains(t, out, "pending:")
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist, "dry run must not create the database")

	out, _, err = runRoot(t, "--config", cfgPath, "db", "migrate")
	require.NoError(t, err)
	require.Contains(t, out, "migrated to version")

	out, _, err = runRoot(t, "--config", cfgPath, "db", "migrate", "--dry-run")
	require.NoError(t, err)
	require.NotContains(t, out, "pending:")
}
