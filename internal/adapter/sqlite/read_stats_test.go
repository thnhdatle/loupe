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

func topIDs(top []query.TopSpan) []string {
	out := make([]string, 0, len(top))
	for _, t := range top {
		out = append(out, t.TraceID+"/"+t.Span.SpanID)
	}
	return out
}

func TestStore_TopSpans(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	writeSpans(t, s, triageTrace(1)...)
	writeSpans(t, s,
		mkSpan(2, 1, 0, trace.KindLLM, model("a"), usage(1, 1), priced(500), lasting(3*time.Second)),
		mkSpan(2, 2, 1, trace.KindLLM, model("b"), usage(2, 2), priced(900)),
		mkSpan(2, 3, 1, trace.KindTool, priced(10_000), lasting(time.Hour)),
		mkSpan(2, 4, 1, trace.KindLLM, model("late"), usage(1, 1), priced(999_999), at(3*time.Hour)))
	r := query.TopSpansRequest{From: anchor, To: anchor.Add(time.Hour), By: query.TopByCost, Limit: 3}

	byCost, err := s.TopSpans(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, []string{
		tid(1).String() + "/" + sid(1).String(),
		tid(2).String() + "/" + sid(2).String(),
		tid(2).String() + "/" + sid(1).String(),
	}, topIDs(byCost))
	require.NotNil(t, byCost[0].Span.CostNanos)
	assert.Equal(t, int64(1234), *byCost[0].Span.CostNanos)

	r.By, r.Limit = query.TopByDuration, 1
	byDuration, err := s.TopSpans(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, []string{tid(2).String() + "/" + sid(1).String()}, topIDs(byDuration))

	_, err = s.TopSpans(ctx, query.TopSpansRequest{From: anchor, To: anchor})
	require.ErrorIs(t, err, query.ErrInvalidRange)
}

func TestStore_ReadRollups_RangeKindAndResolution(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	writeSpans(t, s,
		mkSpan(1, 1, 0, trace.KindAgent, user("u1")),
		mkSpan(1, 2, 1, trace.KindLLM, at(90*time.Minute), usage(1, 2), priced(3)),
		mkSpan(1, 3, 1, trace.KindLLM, at(26*time.Hour), usage(4, 5), priced(6), user("u2")))
	day := rollup.Range{FromNs: anchor.UnixNano(), ToNs: anchor.Add(24 * time.Hour).UnixNano()}

	hours, err := s.ReadRollups(ctx, rollup.ResolutionHour, day, rollup.Filter{})
	require.NoError(t, err)
	buckets := map[int64]trace.Kind{}
	for _, r := range hours {
		buckets[r.Key.BucketNs] = r.Key.Kind
	}
	assert.Equal(t, map[int64]trace.Kind{
		anchor.UnixNano(): trace.KindAgent, anchor.Add(time.Hour).UnixNano(): trace.KindLLM,
	}, buckets, "late spans land in their own hour")

	days, err := s.ReadRollups(ctx, rollup.ResolutionDay,
		rollup.Range{FromNs: anchor.UnixNano(), ToNs: anchor.Add(48 * time.Hour).UnixNano()},
		rollup.Filter{Kind: trace.KindLLM})
	require.NoError(t, err)
	require.Len(t, days, 2)

	users, err := s.ReadUserRollups(ctx, rollup.ResolutionHour,
		rollup.Range{FromNs: anchor.UnixNano(), ToNs: anchor.Add(48 * time.Hour).UnixNano()})
	require.NoError(t, err)
	got := map[string]int64{}
	for _, r := range users {
		got[r.Key.UserID] += r.Aggregate.Measures.Count
	}
	assert.Equal(t, map[string]int64{"u1": 2, "u2": 1}, got)

	_, err = s.ReadRollups(ctx, rollup.Resolution(9), day, rollup.Filter{})
	require.ErrorIs(t, err, rollup.ErrInvalidRange)
	_, err = s.ReadUserRollups(ctx, rollup.Resolution(9), day)
	require.ErrorIs(t, err, rollup.ErrInvalidRange)
}
