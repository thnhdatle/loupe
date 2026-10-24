// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func cacheRead(n int64) spanOpt { return func(s *trace.Span) { s.Usage.CacheRead = n } }

func triageTrace(tr int) []trace.Span {
	return []trace.Span{
		mkSpan(tr, 1, 0, trace.KindLLM, named("chat triage-auto"), model("triage-auto"), usage(100, 50)),
		mkSpan(tr, 2, 1, trace.KindOther, named("POST /v1/chat/completions")),
		mkSpan(tr, 3, 2, trace.KindLLM, named("route request"), model("route-a"), usage(100, 50)),
		mkSpan(tr, 4, 3, trace.KindLLM, named("chat gpt-x"), model("gpt-x"), priced(0), failed()),
		mkSpan(tr, 5, 3, trace.KindLLM, named("chat gpt-x"), model("gpt-x"), usage(100, 50), cacheRead(20),
			priced(1234)),
	}
}

func TestStore_UsageDedupe_TriageChainCountedOnce(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	res := writeSpans(t, s, triageTrace(1)...)
	assert.Equal(t, map[trace.Kind]int{trace.KindLLM: 3}, res.UsageDuplicates)

	d, err := s.GetTrace(ctx, tid(1), false)
	require.NoError(t, err)
	assert.Equal(t, trace.Usage{Input: 100, Output: 50, CacheRead: 20}, d.Summary.Usage)
	assert.Equal(t, int64(1234), d.Summary.CostNanos)
	assert.Zero(t, d.Summary.UnpricedSpans)
	dup := map[string]bool{}
	for _, sp := range d.Spans {
		dup[sp.SpanID] = sp.UsageDuplicate
	}
	assert.Equal(t, map[string]bool{
		sid(1).String(): false, sid(2).String(): false, sid(3).String(): true,
		sid(4).String(): true, sid(5).String(): true,
	}, dup)
	require.NotNil(t, d.Spans[0].CostNanos)
	assert.Equal(t, int64(1234), *d.Spans[0].CostNanos, "the unpriced caller takes its duplicates' cost")

	rows, err := s.ReadRollups(ctx, rollup.ResolutionHour,
		rollup.Range{FromNs: anchor.UnixNano(), ToNs: anchor.Add(time.Hour).UnixNano()}, rollup.Filter{Kind: trace.KindLLM})
	require.NoError(t, err)
	var all rollup.Aggregate
	byModel := map[string]rollup.Measures{}
	for i := range rows {
		all.Merge(&rows[i].Aggregate)
		byModel[rows[i].Key.Model] = rows[i].Aggregate.Measures
	}
	assert.Equal(t, int64(1), all.Measures.Count, "one request per call")
	assert.Equal(t, uint64(4), all.Histogram.Count(), "latency histograms include duplicates")
	assert.Equal(t, int64(1234), byModel["triage-auto"].CostNanos)
	assert.Zero(t, byModel["triage-auto"].Unpriced)
	assert.Zero(t, byModel["gpt-x"].Count)
}

func TestStore_UsageDedupe_ArrivalOrderIndependent(t *testing.T) {
	t.Parallel()
	ref := openStore(t)
	writeSpans(t, ref, triageTrace(1)...)
	want := snapshot(t, ref.Path())

	orders := [][]int{{4, 3, 2, 1, 0}, {0, 2, 4, 1, 3}, {3, 4, 0, 1, 2}, {1, 0, 4, 3, 2}}
	for _, order := range orders {
		s := openStore(t)
		spans := triageTrace(1)
		for _, i := range order {
			writeSpans(t, s, spans[i])
		}
		assert.Equal(t, want, snapshot(t, s.Path()), "order %v", order)
	}
}

func TestStore_UsageDedupe_FlagsSetAndClearedByLaterSpans(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	check := func(step string, u trace.Usage, cost int64, unpriced int, dups ...bool) {
		t.Helper()
		d, err := s.GetTrace(ctx, tid(1), false)
		require.NoError(t, err)
		assert.Equal(t, u, d.Summary.Usage, step)
		assert.Equal(t, cost, d.Summary.CostNanos, step)
		assert.Equal(t, unpriced, d.Summary.UnpricedSpans, step)
		got := make([]bool, 0, len(d.Spans))
		for _, sp := range d.Spans {
			got = append(got, sp.UsageDuplicate)
		}
		assert.Equal(t, dups, got, step)
		sess, err := s.GetSession(ctx, "s", query.Page{})
		require.NoError(t, err)
		assert.Equal(t, u, sess.Summary.Usage, step)
		assert.Equal(t, cost, sess.Summary.CostNanos, step)
	}
	route := mkSpan(1, 1, 0, trace.KindLLM, model("route"), usage(10, 10), session("s"))
	res := writeSpans(t, s, route, mkSpan(1, 2, 1, trace.KindLLM, model("gpt"), usage(6, 6), priced(60)))
	assert.Empty(t, res.UsageDuplicates)
	check("group does not sum", trace.Usage{Input: 16, Output: 16}, 60, 1, false, false)

	res = writeSpans(t, s, mkSpan(1, 3, 1, trace.KindLLM, model("gpt"), usage(4, 4), priced(40)))
	assert.Equal(t, map[trace.Kind]int{trace.KindLLM: 2}, res.UsageDuplicates)
	check("rule 2 flags the group", trace.Usage{Input: 10, Output: 10}, 100, 0, false, true, true)

	res = writeSpans(t, s, mkSpan(1, 4, 1, trace.KindLLM, model("gpt"), usage(1, 1), priced(10)))
	assert.Empty(t, res.UsageDuplicates)
	check("a new member clears the flags", trace.Usage{Input: 21, Output: 21}, 110, 1, false, false, false, false)

	rows, err := s.ReadRollups(ctx, rollup.ResolutionDay,
		rollup.Range{FromNs: anchor.UnixNano(), ToNs: anchor.Add(24 * time.Hour).UnixNano()}, rollup.Filter{})
	require.NoError(t, err)
	var all rollup.Aggregate
	for i := range rows {
		all.Merge(&rows[i].Aggregate)
	}
	assert.Equal(t, rollup.Measures{Count: 4, Usage: trace.Usage{Input: 21, Output: 21}, CostNanos: 110, Unpriced: 1},
		all.Measures)
}
