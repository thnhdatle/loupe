// SPDX-License-Identifier: Apache-2.0

package cost_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/cost"
)

// selectionTable is a small table whose entries differ by input price, so a
// lookup's InputPerMtok identifies the selected entry (in whole USD).
func selectionTable(t *testing.T) *cost.PriceTable {
	t.Helper()
	defaults := []cost.Price{
		entry(t, "exact openai gpt-4o", "1", "0", "2024-05-13"),
		entry(t, "exact openai gpt-4o", "2", "0", "2024-10-01"),
		entry(t, "glob - gpt-4o*", "3", "0", "2024-01-01"),
		entry(t, "glob - gpt-4o-mini*", "4", "0", "2024-01-01"),
		entry(t, "exact - llama-3", "5", "0", "2024-01-01"),
		entry(t, "exact groq llama-3", "6", "0", "2024-01-01"),
		entry(t, "glob - claude-*", "7", "0", "2024-01-01"),
		entry(t, "glob anthropic claude-*", "8", "0", "2024-01-01"),
		entry(t, "exact anthropic claude-sonnet-4", "9", "0", "2025-05-22"),
		entry(t, "glob - mix-*-b", "10", "0", "2024-01-01"),
		entry(t, "glob - mix-a-*", "11", "0", "2024-01-01"),
		entry(t, "exact openai gpt-4o-mini", "12", "0", "2024-07-18"),
		entry(t, "glob - late-*", "13", "0", "2026-01-01"),
		entry(t, "glob - late*", "14", "0", "2024-01-01"),
		entry(t, "glob openai tie-*", "15", "0", "2024-01-01"),
		entry(t, "glob azure tie-*", "16", "0", "2024-01-01"),
	}
	user := []cost.Price{
		entry(t, "glob anthropic claude-sonnet-*", "20", "0", "2025-07-01"),
		entry(t, "exact openai gpt-4o-mini", "21", "0", "2025-03-01"),
		entry(t, "exact - gpt-4.1", "22", "0", "2025-06-01"),
	}
	user[1].Source, user[1].AsOf = "", time.Time{} // user entries need no provenance
	tbl, err := cost.NewPriceTable(defaults, user)
	require.NoError(t, err)
	return tbl
}

func TestPriceTable_Lookup_Selection(t *testing.T) {
	t.Parallel()
	tbl := selectionTable(t)
	at := day("2026-02-01")
	tests := []struct {
		name     string
		provider string
		model    string
		start    time.Time
		want     int64 // whole USD input price of the selected entry; 0 = unpriced
		origin   cost.Origin
	}{
		{name: "exact latest version", provider: "openai", model: "gpt-4o", start: at, want: 2},
		{name: "version boundary inclusive", provider: "openai", model: "gpt-4o", start: day("2024-10-01"), want: 2},
		{
			name: "one nanosecond before boundary", provider: "openai", model: "gpt-4o",
			start: day("2024-10-01").Add(-time.Nanosecond), want: 1,
		},
		{
			name: "exact not yet effective falls to glob", provider: "openai", model: "gpt-4o",
			start: day("2024-03-01"), want: 3,
		},
		{name: "before every version unpriced", provider: "openai", model: "gpt-4o", start: day("2023-12-31")},
		{name: "exact beats glob", provider: "openai", model: "gpt-4o", start: at, want: 2},
		{name: "more literals wins", provider: "openai", model: "gpt-4o-mini-2024", start: at, want: 4},
		{name: "fewer literals when longer misses", provider: "openai", model: "gpt-4o-2024", start: at, want: 3},
		{name: "provider mismatch skips exact", provider: "azure", model: "gpt-4o", start: at, want: 3},
		{name: "provider case-insensitive", provider: "OpenAI", model: "gpt-4o", start: at, want: 2},
		{name: "model case-sensitive", provider: "openai", model: "GPT-4o", start: at},
		{name: "provider-specific exact", provider: "groq", model: "llama-3", start: at, want: 6},
		{name: "provider-less exact", provider: "together", model: "llama-3", start: at, want: 5},
		{name: "empty provider uses provider-less", provider: "", model: "llama-3", start: at, want: 5},
		{name: "provider-specific glob at equal literals", provider: "anthropic", model: "claude-3-opus", start: at, want: 8},
		{name: "provider-less glob for other provider", provider: "aws.bedrock", model: "claude-3-opus", start: at, want: 7},
		{name: "glob tie broken by pattern order", provider: "", model: "mix-a-b", start: at, want: 10},
		{name: "glob tie broken by provider order", provider: "openai", model: "tie-x", start: at, want: 15},
		{name: "glob not effective falls to shorter glob", provider: "", model: "late-x", start: day("2025-12-31"), want: 14},
		{name: "glob effective wins", provider: "", model: "late-x", start: day("2026-01-01"), want: 13},
		{name: "empty model unpriced", provider: "openai", model: "", start: at},
		{name: "unknown model unpriced", provider: "openai", model: "mystery", start: at},
		{
			name: "user glob beats default exact", provider: "anthropic", model: "claude-sonnet-4",
			start: day("2025-08-01"), want: 20, origin: cost.OriginUser,
		},
		{
			name: "user glob not yet effective", provider: "anthropic", model: "claude-sonnet-4",
			start: day("2025-06-30"), want: 9,
		},
		{
			name: "user series replaces default series", provider: "openai", model: "gpt-4o-mini",
			start: at, want: 21, origin: cost.OriginUser,
		},
		{
			name: "replaced default series is gone before user version", provider: "openai", model: "gpt-4o-mini",
			start: day("2025-01-01"), want: 4,
		},
		{
			name: "user provider-less beats default provider-specific", provider: "openai", model: "gpt-4.1",
			start: at, want: 22, origin: cost.OriginUser,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, ok := tbl.Lookup(tc.provider, tc.model, tc.start)
			if tc.want == 0 {
				assert.False(t, ok)
				assert.Equal(t, cost.Price{}, p)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tc.want*cost.NanosPerUSD, p.InputPerMtok)
			wantOrigin := tc.origin
			if wantOrigin == "" {
				wantOrigin = cost.OriginDefault
			}
			assert.Equal(t, wantOrigin, p.Origin)
		})
	}
}

func TestNewPriceTable_Errors(t *testing.T) {
	t.Parallel()
	neg := int64(-1)
	valid := func() cost.Price { return entry(t, "exact - m", "1", "1", "2025-01-01") }
	tests := []struct {
		name     string
		mutate   func(p *cost.Price)
		user     bool
		twice    bool
		want     error
		wantText string
	}{
		{name: "invalid match", mutate: func(p *cost.Price) { p.Match = "regex" }, want: cost.ErrInvalidMatch},
		{name: "empty model", mutate: func(p *cost.Price) { p.Model = "" }, want: cost.ErrEmptyModel},
		{name: "negative input", mutate: func(p *cost.Price) { p.InputPerMtok = -1 }, want: cost.ErrNegativePrice},
		{name: "negative output", mutate: func(p *cost.Price) { p.OutputPerMtok = -1 }, want: cost.ErrNegativePrice},
		{name: "negative reasoning", mutate: func(p *cost.Price) { p.ReasoningPerMtok = &neg }, want: cost.ErrNegativePrice},
		{
			name: "missing effective_from", mutate: func(p *cost.Price) { p.EffectiveFrom = time.Time{} },
			want: cost.ErrMissingEffectiveFrom,
		},
		{name: "default without source", mutate: func(p *cost.Price) { p.Source = "" }, want: cost.ErrMissingProvenance},
		{
			name: "default without as_of", mutate: func(p *cost.Price) { p.AsOf = time.Time{} },
			want: cost.ErrMissingProvenance,
		},
		{name: "duplicate version", mutate: func(*cost.Price) {}, twice: true, want: cost.ErrDuplicateVersion},
		{
			name: "user duplicate version", mutate: func(*cost.Price) {}, twice: true, user: true,
			want: cost.ErrDuplicateVersion, wantText: `cost: user price entry 1 ("m")`,
		},
		{
			name: "user invalid match", mutate: func(p *cost.Price) { p.Match = "" }, user: true,
			want: cost.ErrInvalidMatch, wantText: `cost: user price entry 0 ("m")`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := valid()
			tc.mutate(&p)
			entries := []cost.Price{p}
			if tc.twice {
				entries = append(entries, p)
			}
			var err error
			if tc.user {
				_, err = cost.NewPriceTable(nil, entries)
			} else {
				_, err = cost.NewPriceTable(entries, nil)
			}
			require.ErrorIs(t, err, tc.want)
			var ee *cost.EntryError
			require.ErrorAs(t, err, &ee)
			if tc.wantText != "" {
				assert.Contains(t, err.Error(), tc.wantText)
			}
		})
	}
}

func TestNewPriceTable_CopiesEntries(t *testing.T) {
	t.Parallel()
	cr := usd(t, "0.5")
	p := entry(t, "exact - m", "1", "2", "2025-01-01")
	p.CacheReadPerMtok = &cr
	in := []cost.Price{p}
	tbl, err := cost.NewPriceTable(in, nil)
	require.NoError(t, err)
	cr = 99
	in[0].InputPerMtok = 99
	got, ok := tbl.Lookup("", "m", day("2026-01-01"))
	require.True(t, ok)
	assert.Equal(t, usd(t, "1"), got.InputPerMtok)
	require.NotNil(t, got.CacheReadPerMtok)
	assert.Equal(t, usd(t, "0.5"), *got.CacheReadPerMtok)
}

func TestPriceTable_Entries_MergedAndOrdered(t *testing.T) {
	t.Parallel()
	defaults := []cost.Price{
		entry(t, "exact openai b", "1", "1", "2025-02-01"),
		entry(t, "exact openai b", "1", "1", "2025-01-01"),
		entry(t, "exact openai a", "1", "1", "2025-01-01"),
		entry(t, "exact OpenAI c", "1", "1", "2025-01-01"),
	}
	user := []cost.Price{entry(t, "exact openai c", "2", "2", "2025-03-01")}
	tbl, err := cost.NewPriceTable(defaults, user)
	require.NoError(t, err)
	got := tbl.Entries()
	require.Len(t, got, 4, "the user c series replaces the default one (provider compared case-insensitively)")
	assert.Equal(t, "a", got[0].Model)
	assert.Equal(t, day("2025-01-01"), got[1].EffectiveFrom)
	assert.Equal(t, day("2025-02-01"), got[2].EffectiveFrom)
	assert.Equal(t, cost.OriginUser, got[3].Origin)
	got[0].Model = "mutated"
	assert.Equal(t, "a", tbl.Entries()[0].Model, "Entries returns a copy")
}

func TestEntryError_Unwrap(t *testing.T) {
	t.Parallel()
	err := &cost.EntryError{Origin: cost.OriginDefault, Index: 3, Model: "x", Err: cost.ErrEmptyModel}
	require.ErrorIs(t, err, cost.ErrEmptyModel)
	assert.Equal(t, `cost: default price entry 3 ("x"): cost: empty model`, err.Error())
}

func TestMatch_IsValid(t *testing.T) {
	t.Parallel()
	assert.True(t, cost.MatchExact.IsValid())
	assert.True(t, cost.MatchGlob.IsValid())
	assert.False(t, cost.Match("regex").IsValid())
}
