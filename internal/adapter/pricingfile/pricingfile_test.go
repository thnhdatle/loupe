// SPDX-License-Identifier: Apache-2.0

package pricingfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		check   func(t *testing.T, p []cost.Price)
		wantErr []string
	}{
		{name: "empty file", in: "", check: func(t *testing.T, p []cost.Price) { t.Helper(); assert.Empty(t, p) }},
		{name: "empty list", in: "prices: []\n", check: func(t *testing.T, p []cost.Price) {
			t.Helper()
			assert.Empty(t, p)
		}},
		{
			name: "full entry",
			in: `prices:
  - id: ignored
    match: exact
    provider: openai
    model: gpt-4o
    input_per_mtok: 2.50
    output_per_mtok: 10
    cache_read_per_mtok: 1.25
    cache_write_per_mtok: 0.000000001
    reasoning_per_mtok: 0
    effective_from: 2024-10-01T02:00:00+02:00
    as_of: 2026-10-01
    source: "https://example.test"
`,
			check: func(t *testing.T, p []cost.Price) {
				t.Helper()
				require.Len(t, p, 1)
				e := p[0]
				assert.Equal(t, cost.MatchExact, e.Match)
				assert.Equal(t, int64(2_500_000_000), e.InputPerMtok)
				assert.Equal(t, int64(10_000_000_000), e.OutputPerMtok)
				require.NotNil(t, e.CacheReadPerMtok)
				assert.Equal(t, int64(1_250_000_000), *e.CacheReadPerMtok)
				assert.Equal(t, int64(1), *e.CacheWritePerMtok)
				assert.Equal(t, int64(0), *e.ReasoningPerMtok)
				assert.True(t, e.EffectiveFrom.Equal(time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC)))
				assert.True(t, e.AsOf.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)))
			},
		},
		{name: "optional prices stay nil", in: minimal("x", "1", "2"), check: func(t *testing.T, p []cost.Price) {
			t.Helper()
			assert.Nil(t, p[0].CacheReadPerMtok)
			assert.Nil(t, p[0].ReasoningPerMtok)
		}},
		{name: "unknown key", in: "prices:\n  - {model: a, imput_per_mtok: 1}\n", wantErr: []string{"imput_per_mtok"}},
		{name: "unknown top-level key", in: "price: []\n", wantErr: []string{"price"}},
		{name: "bad yaml", in: "prices: [", wantErr: []string{"invalid pricing file"}},
		{name: "scientific notation", in: minimal("x", "1e3", "2"), wantErr: []string{"input_per_mtok", "invalid price"}},
		{name: "ten decimals", in: minimal("x", "1.0000000001", "2"), wantErr: []string{"fractional digits"}},
		{name: "negative price", in: minimal("x", "-1", "2"), wantErr: []string{"negative"}},
		{
			name:    "missing prices",
			in:      "prices:\n  - {match: exact, model: m, effective_from: 2025-01-01}\n",
			wantErr: []string{"input_per_mtok is required", "output_per_mtok is required"},
		},
		{
			name:    "bad date",
			in:      "prices:\n  - {match: exact, model: m, input_per_mtok: 1, output_per_mtok: 1, effective_from: yesterday}\n",
			wantErr: []string{"effective_from", "yesterday"},
		},
		{name: "scalar expected", in: "prices:\n  - {model: m, input_per_mtok: [1]}\n", wantErr: []string{"want a scalar"}},
		{
			name:    "all entry problems at once",
			in:      minimal("a", "x", "2") + "  - {match: exact, model: b, input_per_mtok: 1}\n",
			wantErr: []string{"entry 0", "entry 1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := pricingfile.Parse(strings.NewReader(tc.in))
			if len(tc.wantErr) > 0 {
				require.ErrorIs(t, err, pricingfile.ErrInvalidFile)
				for _, w := range tc.wantErr {
					assert.Contains(t, err.Error(), w)
				}
				return
			}
			require.NoError(t, err)
			tc.check(t, p)
		})
	}
}

func minimal(model, in, out string) string {
	return "prices:\n  - {match: exact, model: " + model + ", input_per_mtok: " + in + ", output_per_mtok: " + out +
		", effective_from: 2025-01-01}\n"
}

func TestParseKeepsPriceExactness(t *testing.T) {
	t.Parallel()
	p, err := pricingfile.Parse(strings.NewReader(minimal("x", "0.1", "0.3")))
	require.NoError(t, err)
	assert.Equal(t, int64(100_000_000), p[0].InputPerMtok)
	assert.Equal(t, int64(300_000_000), p[0].OutputPerMtok)
}

func TestDefaultsAreAValidTable(t *testing.T) {
	t.Parallel()
	defaults, err := pricingfile.Defaults()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(defaults), 10)
	providers := map[string]bool{}
	for _, p := range defaults {
		assert.NotEmpty(t, p.Source, p.Model)
		assert.False(t, p.AsOf.IsZero(), p.Model)
		providers[p.Provider] = true
	}
	delete(providers, "")
	assert.LessOrEqual(t, len(providers), 6, "the default table covers at most 6 providers")

	table, err := pricingfile.Load("")
	require.NoError(t, err)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	price, ok := table.Lookup("openai", "gpt-4o", at)
	require.True(t, ok)
	assert.Equal(t, cost.OriginDefault, price.Origin)
	nanos, _, err := price.Cost(trace.Usage{Input: 1000, Output: 1000})
	require.NoError(t, err)
	assert.Equal(t, int64(12_500_000), nanos)
	_, ok = table.Lookup("openai", "no-such-model", at)
	assert.False(t, ok)
}

func TestLoadMergesUserFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	user := `prices:
  - {match: exact, provider: openai, model: gpt-4o, input_per_mtok: 1, output_per_mtok: 2, effective_from: 2020-01-01}
  - {match: exact, model: in-house, input_per_mtok: 0.5, output_per_mtok: 0.5, effective_from: 2020-01-01}
`
	require.NoError(t, os.WriteFile(path, []byte(user), 0o600))
	table, err := pricingfile.Load(path)
	require.NoError(t, err)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	p, ok := table.Lookup("openai", "gpt-4o", at)
	require.True(t, ok)
	assert.Equal(t, cost.OriginUser, p.Origin)
	assert.Equal(t, int64(1_000_000_000), p.InputPerMtok, "user series replaces the default series")
	p, ok = table.Lookup("openai", "gpt-4o-mini", at)
	require.True(t, ok)
	assert.Equal(t, cost.OriginDefault, p.Origin, "other defaults stay")
	_, ok = table.Lookup("", "in-house", at)
	assert.True(t, ok)
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	_, err := pricingfile.Load(filepath.Join(dir, "missing.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = pricingfile.Load(write("bad.yaml", "prices:\n  - {model: m, bogus: 1}\n"))
	require.ErrorIs(t, err, pricingfile.ErrInvalidFile)

	_, err = pricingfile.Load(write("nomatch.yaml",
		"prices:\n  - {match: regex, model: m, input_per_mtok: 1, output_per_mtok: 1, effective_from: 2025-01-01}\n"))
	require.ErrorIs(t, err, cost.ErrInvalidMatch)

	_, err = pricingfile.Load(write("dup.yaml", "prices:\n"+
		"  - {match: exact, model: m, input_per_mtok: 1, output_per_mtok: 1, effective_from: 2025-01-01}\n"+
		"  - {match: exact, model: m, input_per_mtok: 2, output_per_mtok: 2, effective_from: 2025-01-01}\n"))
	require.ErrorIs(t, err, cost.ErrDuplicateVersion)

	_, err = pricingfile.Load(write("nodate.yaml",
		"prices:\n  - {match: exact, model: m, input_per_mtok: 1, output_per_mtok: 1}\n"))
	require.ErrorIs(t, err, cost.ErrMissingEffectiveFrom)
}

func TestFixtureDefaultsParse(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../../testdata/cost/cases.yaml")
	require.NoError(t, err)
	start := strings.Index(string(raw), "prices:")
	end := strings.Index(string(raw), "\n  user:")
	require.Positive(t, start)
	require.Greater(t, end, start)
	body := dedent(string(raw)[start+len("prices:\n") : end])
	body = strings.Replace(body, "default:\n", "", 1)
	entries, err := pricingfile.Parse(strings.NewReader("prices:\n" + body))
	require.NoError(t, err)
	assert.Greater(t, len(entries), 20)
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, "  ")
	}
	return strings.Join(lines, "\n")
}
