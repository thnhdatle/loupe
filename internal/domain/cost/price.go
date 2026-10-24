// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"fmt"
	"math"
	"math/bits"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type Match string

const (
	MatchExact Match = "exact"
	MatchGlob  Match = "glob"
)

func (m Match) IsValid() bool { return m == MatchExact || m == MatchGlob }

type Origin string

const (
	OriginDefault Origin = "default"
	OriginUser    Origin = "user"
)

const tokensPerMtok = 1_000_000

type Price struct {
	Match    Match
	Provider string
	Model    string

	InputPerMtok      int64
	OutputPerMtok     int64
	CacheReadPerMtok  *int64
	CacheWritePerMtok *int64
	ReasoningPerMtok  *int64

	EffectiveFrom time.Time
	AsOf          time.Time
	Source        string
	Origin        Origin
}

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
	uncached := u.Input - u.CacheRead
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

func dotRoundHalfUp(counts, rates [5]int64) (nanos int64, ok bool) {
	var hi, lo uint64
	for i := range counts {
		//nolint:gosec // G115: counts and rates are validated non-negative by the caller.
		ph, pl := bits.Mul64(uint64(counts[i]), uint64(rates[i]))
		var carry uint64
		lo, carry = bits.Add64(lo, pl, 0)
		hi += ph + carry
	}
	if hi >= tokensPerMtok {
		return 0, false
	}
	q, r := bits.Div64(hi, lo, tokensPerMtok)
	if q > math.MaxInt64 {
		return 0, false
	}
	if r >= tokensPerMtok/2 {
		q++
	}
	if q > math.MaxInt64 {
		return 0, false
	}
	return int64(q), true
}
