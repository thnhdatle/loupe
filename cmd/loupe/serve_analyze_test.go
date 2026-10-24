// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

func TestNewAnalyzer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(keyFile, []byte("sk-local\n"), 0o600))
	emptyFile := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(emptyFile, nil, 0o600))

	build := func(mod func(c *config.Config)) (bool, error) {
		cfg := config.Default()
		mod(&cfg)
		s := &server{cfg: &cfg, clk: clock.System{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		a, err := s.newAnalyzer()
		if a != nil {
			assert.Equal(t, cfg.Analyze.Timeout.Std(), a.Timeout())
		}
		return a != nil, err
	}
	enable := func(c *config.Config) {
		c.Analyze.BaseURL, c.Analyze.Model = "http://127.0.0.1:11434/v1", "qwen2.5"
		c.Analyze.Timeout = config.Duration(5 * time.Second)
	}

	on, err := build(func(*config.Config) {})
	require.NoError(t, err)
	assert.False(t, on, "off by default, and a nil interface")

	on, err = build(enable)
	require.NoError(t, err)
	assert.True(t, on)

	on, err = build(func(c *config.Config) { enable(c); c.Analyze.APIKeyFile = keyFile })
	require.NoError(t, err)
	assert.True(t, on)

	for _, f := range []string{emptyFile, filepath.Join(dir, "missing")} {
		_, err = build(func(c *config.Config) { enable(c); c.Analyze.APIKeyFile = f })
		require.ErrorContains(t, err, "analyze.api_key_file")
	}
}
