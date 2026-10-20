// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func sid(n byte) trace.SpanID { return trace.SpanID{0, 0, 0, 0, 0, 0, 0, n} }

type us = semconv.UsageSpan

func tok(in, out int64) trace.Usage { return trace.Usage{Input: in, Output: out} }

func span(id, parent byte, kind trace.Kind, u trace.Usage, cost *int64) us {
	s := us{SpanID: sid(id), Kind: kind, Usage: u}
	if parent != 0 {
		s.ParentSpanID = sid(parent)
	}
	if cost != nil {
		s.Cost = trace.Priced(*cost)
	}
	return s
}

func ptr(n int64) *int64 { return &n }

// totals sums the contributions the way trace, session and rollup rows do.
type totals struct {
	calls, unpriced int
	usage           trace.Usage
	cost            int64
	priced          bool
}

func sum(cs []semconv.Contribution) totals {
	var t totals
	for _, c := range cs {
		if !c.Counted() {
			continue
		}
		t.calls++
		t.usage = t.usage.Add(c.Usage)
		if c.Cost.Priced {
			t.cost += c.Cost.Nanos
			t.priced = true
		}
		if c.Unpriced {
			t.unpriced++
		}
	}
	return t
}

func dupFlags(cs []semconv.Contribution) []bool {
	out := make([]bool, len(cs))
	for i, c := range cs {
		out[i] = c.Duplicate
	}
	return out
}

func TestDedupe_Rules(t *testing.T) {
	t.Parallel()
	llm, emb, http := trace.KindLLM, trace.KindEmbedding, trace.KindOther
	tests := []struct {
		name    string
		spans   []us
		wantDup []bool
		want    totals
	}{
		{
			name: "equal usage across skipped http spans",
			spans: []us{
				span(1, 0, llm, tok(100, 20), ptr(500)),
				span(2, 1, http, tok(0, 0), nil),
				span(3, 2, llm, tok(100, 20), ptr(500)),
			},
			wantDup: []bool{false, false, true},
			want:    totals{calls: 2, usage: trace.Usage{Input: 100, Output: 20}, cost: 500, priced: true},
		},
		{
			name: "fallback group: failed attempt without usage plus success",
			spans: []us{
				span(1, 0, llm, tok(100, 20), nil),
				span(2, 1, llm, tok(0, 0), nil),
				span(3, 1, llm, tok(100, 20), ptr(700)),
			},
			wantDup: []bool{false, true, true},
			want:    totals{calls: 1, usage: trace.Usage{Input: 100, Output: 20}, cost: 700, priced: true},
		},
		{
			name: "continuation group: two attempts summing to ancestor",
			spans: []us{
				span(1, 0, llm, tok(100, 30), nil),
				span(2, 1, llm, tok(60, 10), ptr(40)),
				span(3, 1, llm, tok(40, 20), ptr(60)),
			},
			wantDup: []bool{false, true, true},
			want:    totals{calls: 1, usage: trace.Usage{Input: 100, Output: 30}, cost: 100, priced: true},
		},
		{
			name: "group not summing: only the equal span is flagged",
			spans: []us{
				span(1, 0, llm, tok(100, 20), ptr(10)),
				span(2, 1, llm, tok(100, 20), ptr(10)),
				span(3, 1, llm, tok(5, 5), ptr(1)),
			},
			wantDup: []bool{false, true, false},
			want:    totals{calls: 2, usage: trace.Usage{Input: 105, Output: 25}, cost: 11, priced: true},
		},
		{
			name: "ancestor with zero usage flags nothing",
			spans: []us{
				span(1, 0, llm, tok(0, 0), nil),
				span(2, 1, llm, tok(0, 0), nil),
				span(3, 1, llm, tok(10, 1), ptr(3)),
			},
			wantDup: []bool{false, false, false},
			want:    totals{calls: 3, usage: trace.Usage{Input: 10, Output: 1}, cost: 3, priced: true},
		},
		{
			name: "embedding chain",
			spans: []us{
				span(1, 0, emb, tok(50, 0), nil),
				span(2, 1, http, tok(0, 0), nil),
				span(3, 2, emb, tok(50, 0), nil),
				span(4, 3, emb, tok(50, 0), ptr(9)),
			},
			wantDup: []bool{false, false, true, true},
			want:    totals{calls: 2, usage: trace.Usage{Input: 50}, cost: 9, priced: true},
		},
		{
			name: "embedding under an llm chain is its own call",
			spans: []us{
				span(1, 0, llm, tok(100, 20), ptr(5)),
				span(2, 1, emb, tok(100, 20), ptr(2)),
			},
			wantDup: []bool{false, false},
			want: totals{
				calls: 2, usage: trace.Usage{Input: 200, Output: 40}, cost: 7, priced: true,
			},
		},
		{
			name: "cost transfer through an unpriced intermediate",
			spans: []us{
				span(1, 0, llm, tok(100, 20), nil), // triage-auto alias
				span(2, 1, http, tok(0, 0), nil),
				span(3, 2, llm, tok(100, 20), nil), // route request
				span(4, 3, llm, tok(0, 0), nil),    // failed attempt
				span(5, 3, llm, tok(100, 20), ptr(1234)),
			},
			wantDup: []bool{false, false, true, true, true},
			want:    totals{calls: 2, usage: trace.Usage{Input: 100, Output: 20}, cost: 1234, priced: true},
		},
		{
			name: "unpriced kept span stays unpriced when a group span with tokens is unpriced",
			spans: []us{
				span(1, 0, llm, tok(100, 30), nil),
				span(2, 1, llm, tok(60, 10), ptr(40)),
				span(3, 1, llm, tok(40, 20), nil),
			},
			wantDup: []bool{false, true, true},
			want:    totals{calls: 1, unpriced: 1, usage: trace.Usage{Input: 100, Output: 30}},
		},
		{
			name: "priced kept span keeps its own cost",
			spans: []us{
				span(1, 0, llm, tok(100, 20), ptr(1)),
				span(2, 1, llm, tok(100, 20), ptr(999)),
			},
			wantDup: []bool{false, true},
			want:    totals{calls: 1, usage: trace.Usage{Input: 100, Output: 20}, cost: 1, priced: true},
		},
		{
			name: "rule 1 takes the cost of the lowest priced equal span",
			spans: []us{
				span(1, 0, llm, tok(10, 2), nil),
				span(4, 1, llm, tok(10, 2), ptr(40)),
				span(3, 1, llm, tok(10, 2), ptr(30)),
				span(2, 1, llm, tok(10, 2), nil),
			},
			wantDup: []bool{false, true, true, true},
			want:    totals{calls: 1, usage: trace.Usage{Input: 10, Output: 2}, cost: 30, priced: true},
		},
		{
			name: "missing parent stops the walk",
			spans: []us{
				span(1, 0, llm, tok(10, 2), ptr(1)),
				span(3, 2, llm, tok(10, 2), ptr(1)),
			},
			wantDup: []bool{false, false},
			want:    totals{calls: 2, usage: trace.Usage{Input: 20, Output: 4}, cost: 2, priced: true},
		},
		{
			name: "non-llm spans are never compared",
			spans: []us{
				span(1, 0, trace.KindTool, tok(10, 2), nil),
				span(2, 1, trace.KindTool, tok(10, 2), nil),
			},
			wantDup: []bool{false, false},
			want:    totals{calls: 2, unpriced: 2, usage: trace.Usage{Input: 20, Output: 4}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := semconv.Dedupe(tc.spans)
			require.Len(t, got, len(tc.spans))
			assert.Equal(t, tc.wantDup, dupFlags(got))
			assert.Equal(t, tc.want, sum(got))
		})
	}
}

func TestDedupe_ExtrasComeFromDuplicates(t *testing.T) {
	t.Parallel()
	llm := trace.KindLLM
	root := span(1, 0, llm, tok(100, 30), nil)
	a := span(2, 1, llm, tok(60, 10), ptr(1))
	a.Usage.CacheRead, a.Usage.Reasoning = 20, 4
	b := span(3, 1, llm, tok(40, 20), ptr(1))
	b.Usage.CacheRead, b.Usage.CacheWrite = 5, 7
	got := semconv.Dedupe([]us{root, a, b})
	assert.Equal(t, trace.Usage{Input: 100, Output: 30, CacheRead: 25, CacheWrite: 7, Reasoning: 4}, got[0].Usage)

	own := span(1, 0, llm, tok(100, 20), nil)
	own.Usage.CacheRead = 50
	dup := span(2, 1, llm, tok(100, 20), ptr(1))
	dup.Usage.CacheRead, dup.Usage.Reasoning = 10, 3
	got = semconv.Dedupe([]us{own, dup})
	assert.Equal(t, trace.Usage{Input: 100, Output: 20, CacheRead: 50, Reasoning: 3}, got[0].Usage)
}

func TestDedupe_CyclicParentsTerminate(t *testing.T) {
	t.Parallel()
	llm := trace.KindLLM
	got := semconv.Dedupe([]us{
		span(1, 2, llm, tok(10, 1), ptr(1)),
		span(2, 1, llm, tok(10, 1), ptr(1)),
		span(3, 3, llm, tok(10, 1), ptr(1)), // its own parent
	})
	require.Len(t, got, 3)
	// Span 1 has the lowest id in the cycle and loses its ancestor: it is kept,
	// span 2 duplicates it.
	assert.Equal(t, []bool{false, true, false}, dupFlags(got))
}

func TestDedupe_Empty(t *testing.T) {
	t.Parallel()
	assert.Empty(t, semconv.Dedupe(nil))
}

func genTraceSpans(t *rapid.T) []us {
	n := rapid.IntRange(1, 12).Draw(t, "n")
	kinds := []trace.Kind{trace.KindLLM, trace.KindEmbedding, trace.KindOther}
	spans := make([]us, n)
	for i := range spans {
		id := byte(i + 1)
		var parent byte
		if i > 0 && rapid.IntRange(0, 4).Draw(t, "root") > 0 {
			parent = byte(rapid.IntRange(1, i).Draw(t, "parent"))
		}
		in := rapid.SampledFrom([]int64{0, 10, 20, 30}).Draw(t, "in")
		out := rapid.SampledFrom([]int64{0, 5, 10}).Draw(t, "out")
		var cost *int64
		if rapid.Bool().Draw(t, "priced") {
			cost = ptr(rapid.Int64Range(0, 100).Draw(t, "cost"))
		}
		spans[i] = span(id, parent, rapid.SampledFrom(kinds).Draw(t, "kind"), tok(in, out), cost)
		spans[i].Usage.CacheRead = rapid.SampledFrom([]int64{0, 3}).Draw(t, "cr")
	}
	return spans
}

// Aggregates depend only on the set of stored spans, not on arrival order.
func TestDedupe_OrderIndependenceProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		spans := genTraceSpans(t)
		perm := rapid.Permutation(spans).Draw(t, "perm")
		base := map[trace.SpanID]semconv.Contribution{}
		for i, c := range semconv.Dedupe(spans) {
			base[spans[i].SpanID] = c
		}
		for i, c := range semconv.Dedupe(perm) {
			if base[perm[i].SpanID] != c {
				t.Fatalf("span %s: %+v vs %+v", perm[i].SpanID, c, base[perm[i].SpanID])
			}
		}
	})
}

// Each duplicated call is counted once: totals never exceed the plain sum, and
// a kept span's input and output are its own.
func TestDedupe_NeverInflatesProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		spans := genTraceSpans(t)
		var plain trace.Usage
		for _, s := range spans {
			plain = plain.Add(s.Usage)
		}
		got := semconv.Dedupe(spans)
		total := sum(got)
		if total.usage.Input > plain.Input || total.usage.Output > plain.Output {
			t.Fatalf("totals %+v exceed plain sums %+v", total.usage, plain)
		}
		for i, c := range got {
			if c.Counted() && (c.Usage.Input != spans[i].Usage.Input || c.Usage.Output != spans[i].Usage.Output) {
				t.Fatalf("kept span %d changed its input/output", i)
			}
		}
	})
}
