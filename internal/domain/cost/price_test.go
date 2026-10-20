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

func TestPrice_Cost_Formula(t *testing.T) {
	t.Parallel()
	full := cost.Price{
		InputPerMtok: usd(t, "3"), OutputPerMtok: usd(t, "15"),
		CacheReadPerMtok: opt(t, "0.30"), CacheWritePerMtok: opt(t, "3.75"), ReasoningPerMtok: opt(t, "20"),
	}
	bare := cost.Price{InputPerMtok: usd(t, "2"), OutputPerMtok: usd(t, "8")}
	half := cost.Price{InputPerMtok: usd(t, "0.0000005"), OutputPerMtok: usd(t, "0.0000015")}
	tests := []struct {
		name    string
		price   cost.Price
		usage   trace.Usage
		want    int64
		clamped bool
	}{
		{name: "input and output", price: bare, usage: trace.Usage{Input: 1000, Output: 100}, want: 2_800_000},
		{
			// 3000·3e9 + 2000·0.3e9 + 5000·3.75e9 + 500·15e9 = 35.85e12 → /1e6
			name: "anthropic cache read and write", price: full,
			usage: trace.Usage{Input: 10000, Output: 500, CacheRead: 2000, CacheWrite: 5000}, want: 35_850_000,
		},
		{
			name: "cache prices fall back to input", price: bare,
			usage: trace.Usage{Input: 1000, CacheRead: 300, CacheWrite: 200}, want: 2_000_000,
		},
		{
			// 100·15e9 + 300·20e9 = 7.5e12
			name: "reasoning with price", price: full,
			usage: trace.Usage{Output: 400, Reasoning: 300}, want: 7_500_000,
		},
		{
			name: "reasoning without price uses output", price: bare,
			usage: trace.Usage{Output: 400, Reasoning: 300}, want: 3_200_000,
		},
		{
			// uncached clamps to 0: 80·0.3e9 + 50·3.75e9 = 211.5e9
			name: "cache exceeds input clamps", price: full,
			usage: trace.Usage{Input: 100, CacheRead: 80, CacheWrite: 50}, want: 211_500, clamped: true,
		},
		{
			name: "cache read alone exceeds input clamps", price: full,
			usage: trace.Usage{Input: 10, CacheRead: 11}, want: 3_300, clamped: true,
		},
		{
			name: "reasoning exceeds output clamps", price: bare,
			usage: trace.Usage{Output: 10, Reasoning: 20}, want: 160_000, clamped: true,
		},
		{
			name: "no clamp at equality", price: bare, usage: trace.Usage{Input: 5, CacheRead: 5, Output: 5, Reasoning: 5},
			want: 50_000,
		},
		{name: "zero usage", price: full, usage: trace.Usage{}, want: 0},
		{name: "half nano rounds up", price: half, usage: trace.Usage{Input: 1000}, want: 1},
		{name: "below half rounds down", price: half, usage: trace.Usage{Input: 999}, want: 0},
		{name: "two and a half rounds up", price: half, usage: trace.Usage{Input: 5000}, want: 3},
		{name: "single rounding per span", price: half, usage: trace.Usage{Input: 1000, Output: 333}, want: 1},
		{name: "sub-micro cost", price: bare, usage: trace.Usage{Input: 1}, want: 2_000},
		{
			name:  "max price one million tokens is max int64",
			price: cost.Price{InputPerMtok: math.MaxInt64}, usage: trace.Usage{Input: 1_000_000}, want: math.MaxInt64,
		},
		{
			// (4·MaxInt64 + 1) / 1e6 = 36893488147419.103229; the numerator exceeds 2⁶⁴
			name:  "128-bit numerator",
			price: cost.Price{InputPerMtok: math.MaxInt64, OutputPerMtok: 1}, usage: trace.Usage{Input: 4, Output: 1},
			want: 36_893_488_147_419,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, flags, err := tc.price.Cost(tc.usage)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.clamped, flags.Has(trace.FlagUsageClamped))
			assert.Equal(t, trace.Flags(0), flags&^trace.FlagUsageClamped, "only the clamped bit is set")
		})
	}
}

func TestPrice_Cost_Errors(t *testing.T) {
	t.Parallel()
	maxed := cost.Price{
		InputPerMtok: math.MaxInt64, OutputPerMtok: math.MaxInt64,
	}
	neg := int64(-1)
	tests := []struct {
		name  string
		price cost.Price
		usage trace.Usage
		want  error
	}{
		{name: "negative input", price: maxed, usage: trace.Usage{Input: -1}, want: cost.ErrNegativeUsage},
		{name: "negative reasoning", price: maxed, usage: trace.Usage{Reasoning: -1}, want: cost.ErrNegativeUsage},
		{name: "negative cache write", price: maxed, usage: trace.Usage{CacheWrite: -1}, want: cost.ErrNegativeUsage},
		{name: "negative price", price: cost.Price{CacheReadPerMtok: &neg}, want: cost.ErrNegativePrice},
		{
			name: "quotient one past max int64", price: cost.Price{InputPerMtok: math.MaxInt64},
			usage: trace.Usage{Input: 1_000_001}, want: cost.ErrCostOverflow,
		},
		{
			name: "rounding past max int64", price: cost.Price{InputPerMtok: math.MaxInt64, OutputPerMtok: 500_000},
			usage: trace.Usage{Input: 1_000_000, Output: 1}, want: cost.ErrCostOverflow,
		},
		{
			name: "quotient at or above 2^64", price: maxed,
			usage: trace.Usage{Input: math.MaxInt64}, want: cost.ErrCostOverflow,
		},
		{
			name: "two maximal products", price: maxed,
			usage: trace.Usage{Input: math.MaxInt64, Output: math.MaxInt64}, want: cost.ErrCostOverflow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := tc.price.Cost(tc.usage)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// The largest possible numerator (3·MaxInt64², reached only with clamped
// usage) must report an overflow, not wrap the 128-bit accumulator or panic.
func TestPrice_Cost_LargestNumerator(t *testing.T) {
	t.Parallel()
	m := int64(math.MaxInt64)
	p := cost.Price{
		InputPerMtok: m, OutputPerMtok: m, CacheReadPerMtok: &m, CacheWritePerMtok: &m, ReasoningPerMtok: &m,
	}
	_, flags, err := p.Cost(trace.Usage{Input: m, Output: m, CacheRead: m, CacheWrite: m, Reasoning: m})
	require.ErrorIs(t, err, cost.ErrCostOverflow)
	assert.True(t, flags.Has(trace.FlagUsageClamped))
}
