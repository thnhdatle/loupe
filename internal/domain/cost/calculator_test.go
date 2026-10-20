// SPDX-License-Identifier: Apache-2.0

package cost_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func newCalculator(t *testing.T) *cost.Calculator {
	t.Helper()
	gpt := entry(t, "exact openai gpt-4o", "2.50", "10", "2024-10-01")
	gpt.CacheReadPerMtok = opt(t, "1.25")
	sonnet := entry(t, "exact anthropic claude-sonnet-4", "3", "15", "2025-05-22")
	sonnet.CacheReadPerMtok, sonnet.CacheWritePerMtok = opt(t, "0.30"), opt(t, "3.75")
	gemini := entry(t, "exact google gemini-2.5-flash", "0.30", "2.50", "2025-04-17")
	gemini.ReasoningPerMtok = opt(t, "3.50")
	maxP := entry(t, "exact - max-price", "1", "1", "2025-01-01")
	maxP.InputPerMtok = math.MaxInt64
	tbl, err := cost.NewPriceTable([]cost.Price{gpt, sonnet, gemini, maxP}, nil)
	require.NoError(t, err)
	calc, err := cost.NewCalculator(tbl)
	require.NoError(t, err)
	return calc
}

func TestCalculator_Cost_Spans(t *testing.T) {
	t.Parallel()
	calc := newCalculator(t)
	at := day("2026-01-01")
	tests := []struct {
		name    string
		in      cost.Input
		want    trace.Cost
		model   string
		clamped bool
	}{
		{
			name: "response model priced",
			in: cost.Input{
				Provider: "openai", ResponseModel: "gpt-4o", Start: at, Usage: trace.Usage{Input: 1200, Output: 300},
			},
			want: trace.Priced(6_000_000), model: "gpt-4o",
		},
		{
			name: "request model fallback when response empty",
			in: cost.Input{
				Provider: "openai", RequestModel: "gpt-4o", Start: at, Usage: trace.Usage{Input: 1200, Output: 300},
			},
			want: trace.Priced(6_000_000), model: "gpt-4o",
		},
		{
			name: "unpriced response model does not fall back",
			in: cost.Input{
				Provider: "openai", ResponseModel: "my-ft", RequestModel: "gpt-4o", Start: at,
				Usage: trace.Usage{Input: 10},
			},
			want: trace.Cost{}, model: "my-ft",
		},
		{
			name: "anthropic cache write",
			in: cost.Input{
				Provider: "anthropic", ResponseModel: "claude-sonnet-4", Start: at,
				Usage: trace.Usage{Input: 10000, Output: 500, CacheRead: 2000, CacheWrite: 5000},
			},
			want: trace.Priced(35_850_000), model: "claude-sonnet-4",
		},
		{
			// 1500·0.3e9 + 800·2.5e9 + 3200·3.5e9 = 13.65e12
			name: "reasoning price",
			in: cost.Input{
				Provider: "google", ResponseModel: "gemini-2.5-flash", Start: at,
				Usage: trace.Usage{Input: 1500, Output: 4000, Reasoning: 3200},
			},
			want: trace.Priced(13_650_000), model: "gemini-2.5-flash",
		},
		{
			name: "clamped",
			in: cost.Input{
				Provider: "openai", ResponseModel: "gpt-4o", Start: at,
				Usage: trace.Usage{Input: 100, CacheRead: 120},
			},
			want: trace.Priced(150_000), model: "gpt-4o", clamped: true,
		},
		{
			name: "priced without usage costs zero",
			in:   cost.Input{Provider: "openai", ResponseModel: "gpt-4o", Start: at},
			want: trace.Priced(0), model: "gpt-4o",
		},
		{
			name: "no model is unpriced",
			in:   cost.Input{Provider: "openai", Start: at, Usage: trace.Usage{Input: 5}},
			want: trace.Cost{}, model: "",
		},
		{
			name: "before effective_from is unpriced",
			in: cost.Input{
				Provider: "openai", ResponseModel: "gpt-4o", Start: day("2024-09-30"), Usage: trace.Usage{Input: 5},
			},
			want: trace.Cost{}, model: "gpt-4o",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := calc.Cost(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, res.Cost)
			assert.Equal(t, tc.model, res.Model)
			assert.Equal(t, tc.clamped, res.Flags.Has(trace.FlagUsageClamped))
			if !tc.want.Priced {
				assert.Equal(t, cost.Price{}, res.Price)
				assert.Zero(t, res.Flags)
			}
		})
	}
}

func TestCalculator_Cost_Errors(t *testing.T) {
	t.Parallel()
	calc := newCalculator(t)
	at := day("2026-01-01")
	_, err := calc.Cost(cost.Input{ResponseModel: "max-price", Start: at, Usage: trace.Usage{Input: 1_000_001}})
	require.ErrorIs(t, err, cost.ErrCostOverflow)
	assert.Contains(t, err.Error(), `"max-price"`)
	_, err = calc.Cost(cost.Input{Provider: "openai", ResponseModel: "gpt-4o", Start: at, Usage: trace.Usage{Output: -1}})
	require.ErrorIs(t, err, cost.ErrNegativeUsage)
}

func TestNewCalculator_NilTable(t *testing.T) {
	t.Parallel()
	_, err := cost.NewCalculator(nil)
	require.ErrorIs(t, err, cost.ErrNilTable)
}

func TestPricingModel_Fallback(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "resp", cost.PricingModel("resp", "req"))
	assert.Equal(t, "req", cost.PricingModel("", "req"))
	assert.Empty(t, cost.PricingModel("", ""))
}
