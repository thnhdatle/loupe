// SPDX-License-Identifier: Apache-2.0

package cost_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// maxTokens keeps every generated cost below MaxInt64 for prices up to maxPrice.
const (
	maxTokens = int64(1) << 40
	maxPrice  = int64(1) << 36 // ≈ 68.7 USD per Mtok
)

func genPrice(t *rapid.T) cost.Price {
	n := rapid.Int64Range(0, maxPrice)
	optional := func(label string) *int64 {
		if !rapid.Bool().Draw(t, label+".set") {
			return nil
		}
		v := n.Draw(t, label)
		return &v
	}
	return cost.Price{
		InputPerMtok: n.Draw(t, "in"), OutputPerMtok: n.Draw(t, "out"),
		CacheReadPerMtok: optional("cr"), CacheWritePerMtok: optional("cw"), ReasoningPerMtok: optional("rs"),
	}
}

// genInclusiveUsage draws usage with inclusive semantics: cache tokens are
// part of input and reasoning tokens part of output, so nothing clamps.
func genInclusiveUsage(t *rapid.T) trace.Usage {
	in := rapid.Int64Range(0, maxTokens).Draw(t, "input")
	cr := rapid.Int64Range(0, in).Draw(t, "cache_read")
	cw := rapid.Int64Range(0, in-cr).Draw(t, "cache_write")
	out := rapid.Int64Range(0, maxTokens).Draw(t, "output")
	rs := rapid.Int64Range(0, out).Draw(t, "reasoning")
	return trace.Usage{Input: in, Output: out, CacheRead: cr, CacheWrite: cw, Reasoning: rs}
}

func mustCost(t *rapid.T, p *cost.Price, u trace.Usage) (int64, trace.Flags) {
	n, f, err := p.Cost(u)
	if err != nil {
		t.Fatalf("Cost(%+v) at %+v: %v", u, p, err)
	}
	return n, f
}

// Cost is never negative, for any usage including clamped usage.
func TestPrice_Cost_NeverNegativeProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		p := genPrice(t)
		n := rapid.Int64Range(0, maxTokens)
		u := trace.Usage{
			Input: n.Draw(t, "in"), Output: n.Draw(t, "out"),
			CacheRead: n.Draw(t, "cr"), CacheWrite: n.Draw(t, "cw"), Reasoning: n.Draw(t, "rs"),
		}
		if got, _ := mustCost(t, &p, u); got < 0 {
			t.Fatalf("cost %d < 0", got)
		}
	})
}

// Cost is monotonic in each token count over valid inclusive usage.
// Input and output are raised with the other counts fixed. A detail count
// (cache read, cache write, reasoning) is raised together with the total that
// includes it, since an extra cached token is an extra input token; raising it
// with input fixed would move tokens to a possibly cheaper price instead.
func TestPrice_Cost_MonotonicProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		p := genPrice(t)
		u := genInclusiveUsage(t)
		d := rapid.Int64Range(0, maxTokens).Draw(t, "delta")
		field := rapid.SampledFrom([]string{"input", "output", "cache_read", "cache_write", "reasoning"}).Draw(t, "field")
		v := u
		switch field {
		case "input":
			v.Input += d
		case "output":
			v.Output += d
		case "cache_read":
			v.Input, v.CacheRead = v.Input+d, v.CacheRead+d
		case "cache_write":
			v.Input, v.CacheWrite = v.Input+d, v.CacheWrite+d
		case "reasoning":
			v.Output, v.Reasoning = v.Output+d, v.Reasoning+d
		}
		before, fb := mustCost(t, &p, u)
		after, fa := mustCost(t, &p, v)
		if fb != 0 || fa != 0 {
			t.Fatalf("inclusive usage clamped: %v %v", fb, fa)
		}
		if after < before {
			t.Fatalf("raising %s by %d lowered cost %d -> %d", field, d, before, after)
		}
	})
}

// Raising input or output with every other count fixed never lowers the
// cost, for arbitrary (possibly clamped) usage: each max(0, …) term is
// non-decreasing in the total it subtracts from.
func TestPrice_Cost_MonotonicInTotalsProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		p := genPrice(t)
		n := rapid.Int64Range(0, maxTokens)
		u := trace.Usage{
			Input: n.Draw(t, "in"), Output: n.Draw(t, "out"),
			CacheRead: n.Draw(t, "cr"), CacheWrite: n.Draw(t, "cw"), Reasoning: n.Draw(t, "rs"),
		}
		d := n.Draw(t, "delta")
		before, _ := mustCost(t, &p, u)
		v := u
		if rapid.Bool().Draw(t, "output") {
			v.Output += d
		} else {
			v.Input += d
		}
		if after, _ := mustCost(t, &p, v); after < before {
			t.Fatalf("raising a total by %d lowered cost %d -> %d", d, before, after)
		}
	})
}
