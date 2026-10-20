// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"fmt"
	"math"
	"math/bits"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Match says how a price entry's Model is compared with a span's pricing model.
type Match string

// Match kinds.
const (
	// MatchExact compares the model byte for byte (case-sensitive).
	MatchExact Match = "exact"
	// MatchGlob treats the model as a glob pattern (see MatchGlobPattern).
	MatchGlob Match = "glob"
)

// IsValid reports whether m is a defined match kind.
func (m Match) IsValid() bool { return m == MatchExact || m == MatchGlob }

// Origin says which price file an entry came from.
type Origin string

// Origins. User entries replace default series with the same key and win selection.
const (
	OriginDefault Origin = "default"
	OriginUser    Origin = "user"
)

// tokensPerMtok is the divisor that turns token·(nano-USD per Mtok) into nano-USD.
const tokensPerMtok = 1_000_000

// Price is one version of a price series: USD per million tokens held as
// integer nano-USD per million tokens (see ParsePrice).
//
// A nil optional price falls back (docs/guides/pricing.md): cache read and
// cache write to InputPerMtok, reasoning to OutputPerMtok.
type Price struct {
	Match    Match
	Provider string // empty: applies to every provider
	Model    string // model id (exact) or pattern (glob)

	InputPerMtok      int64
	OutputPerMtok     int64
	CacheReadPerMtok  *int64
	CacheWritePerMtok *int64
	ReasoningPerMtok  *int64

	EffectiveFrom time.Time // first instant (inclusive) at which this version applies
	AsOf          time.Time // when the price was last checked against Source
	Source        string
	Origin        Origin // set by NewPriceTable
}

// Cost computes the cost in nano-USD of usage u at price p:
//
//	uncached_in = max(0, input − cache_read − cache_write)
//	plain_out   = max(0, output − reasoning)
//	numerator   = uncached_in·P_in + cache_read·(P_cache_read ?? P_in)
//	            + cache_write·(P_cache_write ?? P_in)
//	            + plain_out·P_out + reasoning·(P_reasoning ?? P_out)
//	cost_nanos  = round_half_up(numerator / 10⁶)
//
// The numerator is accumulated exactly in 128 bits and rounded once. The
// returned flags hold trace.FlagUsageClamped when a max(0, …) clamped.
// It returns ErrNegativeUsage for a negative count or price and
// ErrCostOverflow when the result does not fit in an int64.
func (p *Price) Cost(u trace.Usage) (int64, trace.Flags, error) {
	if u.Input < 0 || u.Output < 0 || u.CacheRead < 0 || u.CacheWrite < 0 || u.Reasoning < 0 {
		return 0, 0, fmt.Errorf("cost: usage %+v: %w", u, ErrNegativeUsage)
	}
	in, out := p.InputPerMtok, p.OutputPerMtok
	rates := [5]int64{
		in, orDefault(p.CacheReadPerMtok, in), orDefault(p.CacheWritePerMtok, in), out,
		orDefault(p.ReasoningPerMtok, out),
	}
	for _, r := range rates {
		if r < 0 {
			return 0, 0, fmt.Errorf("cost: price %d: %w", r, ErrNegativePrice)
		}
	}
	var flags trace.Flags
	uncached := u.Input - u.CacheRead // both ≥ 0: cannot overflow
	if uncached >= 0 {
		uncached -= u.CacheWrite
	}
	if uncached < 0 {
		uncached, flags = 0, trace.FlagUsageClamped
	}
	plainOut := u.Output - u.Reasoning
	if plainOut < 0 {
		plainOut, flags = 0, trace.FlagUsageClamped
	}
	counts := [5]int64{uncached, u.CacheRead, u.CacheWrite, plainOut, u.Reasoning}
	nanos, ok := dotRoundHalfUp(counts, rates)
	if !ok {
		return 0, flags, fmt.Errorf("cost: usage %+v: %w", u, ErrCostOverflow)
	}
	return nanos, flags, nil
}

func orDefault(p *int64, def int64) int64 {
	if p == nil {
		return def
	}
	return *p
}

// dotRoundHalfUp returns round_half_up(Σ counts[i]·rates[i] / 10⁶) for
// non-negative inputs, or ok = false when the result exceeds MaxInt64.
//
// The sum cannot overflow the 128-bit accumulator: with every rate ≤ M =
// 2⁶³−1, the input terms total at most max(input, cache_read+cache_write)·M ≤
// 2M² and the output terms at most max(output, reasoning)·M ≤ M², so the
// numerator is below 3M² < 2¹²⁸.
func dotRoundHalfUp(counts, rates [5]int64) (nanos int64, ok bool) {
	var hi, lo uint64
	for i := range counts {
		//nolint:gosec // G115: counts and rates are validated non-negative by the caller.
		ph, pl := bits.Mul64(uint64(counts[i]), uint64(rates[i]))
		var carry uint64
		lo, carry = bits.Add64(lo, pl, 0)
		hi += ph + carry // cannot wrap: see the bound above
	}
	if hi >= tokensPerMtok { // quotient would be ≥ 2⁶⁴ (and Div64 would panic)
		return 0, false
	}
	q, r := bits.Div64(hi, lo, tokensPerMtok)
	if q > math.MaxInt64 {
		return 0, false
	}
	if r >= tokensPerMtok/2 {
		q++ // q ≤ MaxInt64 before the increment, so it cannot wrap
	}
	if q > math.MaxInt64 {
		return 0, false
	}
	return int64(q), true
}
