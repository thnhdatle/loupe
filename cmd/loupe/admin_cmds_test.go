// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/cost"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func writeCfgYAML(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "loupe.yaml")
	require.NoError(t, os.WriteFile(p, []byte(yaml), 0o600))
	return p
}

func hostPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return u.Host
}

func TestHealthcheck_Status(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		ok     bool
	}{{"ready", http.StatusOK, true}, {"not ready", http.StatusServiceUnavailable, false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/readyz", r.URL.Path)
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			cfg := writeCfgYAML(t, "admin:\n  listen: \""+hostPort(t, srv)+"\"\n")
			_, err := runCmd(t, "--config", cfg, "healthcheck")
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, errUnhealthy)
		})
	}
}

func TestHealthcheck_ConnectionRefused(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := hostPort(t, srv)
	srv.Close()
	cfg := writeCfgYAML(t, "admin:\n  listen: \""+addr+"\"\n")
	_, err := runCmd(t, "--config", cfg, "healthcheck")
	require.ErrorIs(t, err, errUnhealthy)
}

func TestDialHost(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"0.0.0.0:9092":     "127.0.0.1:9092",
		":3000":            "127.0.0.1:3000",
		"[::]:3000":        "127.0.0.1:3000",
		"127.0.0.1:9092":   "127.0.0.1:9092",
		"example.com:3000": "example.com:3000",
	} {
		got, err := dialHost(in)
		require.NoError(t, err)
		assert.Equal(t, want, got, in)
	}
	_, err := dialHost("nonsense")
	require.Error(t, err)
}

type fakeAdmin struct {
	t     *testing.T
	fail  bool
	polls atomic.Int32
	body  atomic.Value
	post  atomic.Value
}

func (f *fakeAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer secret" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad token"}}`))
		return
	}
	switch {
	case r.Method == http.MethodPost:
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.body.Store(b)
		f.post.Store(r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"job_id":"job_1"}`))
	case r.URL.Path == "/v1/admin/jobs/job_1":
		state, errText := "running", ""
		if f.polls.Add(1) > 1 {
			state = "done"
			if f.fail {
				state, errText = "failed", "boom"
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": "job_1", "state": state, "rows": 7, "error": errText})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func adminConfig(t *testing.T) (cfgPath, tokenPath string) {
	t.Helper()
	tokenPath = filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("secret\n"), 0o600))
	return writeCfgYAML(t, "api:\n  token_file: \""+tokenPath+"\"\n"), tokenPath
}

func TestDBVacuum_Online(t *testing.T) {
	t.Parallel()
	fa := &fakeAdmin{t: t}
	srv := httptest.NewServer(fa)
	defer srv.Close()
	cfg, _ := adminConfig(t)
	out, err := runCmd(t, "--config", cfg, "db", "vacuum", "--server", srv.URL)
	require.NoError(t, err)
	assert.Contains(t, out, "job_1")
	assert.Contains(t, out, "7 pages")
	assert.Equal(t, "/v1/admin/vacuum", fa.post.Load())
}

func TestDBVacuum_OnlineTokenFileFlagAndFailure(t *testing.T) {
	t.Parallel()
	fa := &fakeAdmin{t: t, fail: true}
	srv := httptest.NewServer(fa)
	defer srv.Close()
	_, tok := adminConfig(t)
	_, err := runCmd(t, "db", "vacuum", "--server", srv.URL, "--token-file", tok)
	require.ErrorIs(t, err, errAdminAPI)
	assert.Contains(t, err.Error(), "boom")

	bad := filepath.Join(t.TempDir(), "bad")
	require.NoError(t, os.WriteFile(bad, []byte("nope\n"), 0o600))
	_, err = runCmd(t, "db", "vacuum", "--server", srv.URL, "--token-file", bad)
	require.ErrorIs(t, err, errAdminAPI)
	assert.Contains(t, err.Error(), "unauthorized")
}

func TestDBVacuum_FullRequiresOffline(t *testing.T) {
	t.Parallel()
	_, err := runCmd(t, "db", "vacuum", "--full")
	require.ErrorIs(t, err, errUsage)
}

func TestDBVacuum_ServerDown(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := runCmd(t, "db", "vacuum", "--server", url)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--offline")
}

func TestPricingRecompute_Online(t *testing.T) {
	t.Parallel()
	fa := &fakeAdmin{t: t}
	srv := httptest.NewServer(fa)
	defer srv.Close()
	cfg, _ := adminConfig(t)
	out, err := runCmd(t, "--config", cfg, "pricing", "recompute", "--server", srv.URL,
		"--since", "2026-01-02", "--until", "2026-02-03T04:05:06Z")
	require.NoError(t, err)
	assert.Contains(t, out, "7 spans")
	assert.Equal(t, "/v1/admin/pricing/recompute", fa.post.Load())
	assert.Equal(t, map[string]string{"since": "2026-01-02T00:00:00Z", "until": "2026-02-03T04:05:06Z"}, fa.body.Load())
}

func TestPricingRecompute_BadArgs(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"pricing", "recompute"},
		{"pricing", "recompute", "--since", "yesterday"},
		{"pricing", "recompute", "--since", "2026-02-01", "--until", "2026-01-01"},
	} {
		_, err := runCmd(t, args...)
		require.ErrorIs(t, err, errUsage, args)
	}
}

func TestParseDate(t *testing.T) {
	t.Parallel()
	got, err := parseDate("2026-03-04")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC), got)
	got, err = parseDate("2026-03-04T05:00:00+02:00")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 4, 3, 0, 0, 0, time.UTC), got)
}

func TestPricingList(t *testing.T) {
	t.Parallel()
	user := filepath.Join(t.TempDir(), "prices.yaml")
	require.NoError(t, os.WriteFile(user, []byte(`prices:
  - match: exact
    provider: acme
    model: old-model
    input_per_mtok: 1.5
    output_per_mtok: 3
    effective_from: 2020-01-01T00:00:00Z
    as_of: 2020-01-01T00:00:00Z
    source: "https://example.com/pricing"
`), 0o600))
	cfg := writeCfgYAML(t, "pricing:\n  file: \""+user+"\"\n")

	out, err := runCmd(t, "--config", cfg, "pricing", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "old-model")
	assert.Contains(t, out, "STALE")
	assert.Contains(t, out, "gpt-4o")

	out, err = runCmd(t, "--config", cfg, "pricing", "list", "--json")
	require.NoError(t, err)
	var rows []priceRow
	require.NoError(t, json.Unmarshal([]byte(out), &rows))
	var found bool
	for _, r := range rows {
		if r.Model == "old-model" {
			found = true
			assert.Equal(t, "user", r.Origin)
			assert.True(t, r.Stale)
			assert.Equal(t, "1.5", strings.TrimSuffix(r.Input, "0"))
		}
	}
	assert.True(t, found)
}

func TestPriceRows_StaleBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	rows := priceRows([]cost.Price{
		{Model: "a", AsOf: now.AddDate(0, 0, -90)},
		{Model: "b", AsOf: now.AddDate(0, 0, -91)},
	}, now)
	assert.False(t, rows[0].Stale)
	assert.True(t, rows[1].Stale)
	assert.Equal(t, 91, rows[1].AgeDays)
}
