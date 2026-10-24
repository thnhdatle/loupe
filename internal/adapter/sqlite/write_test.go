// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func simpleTrace(tr int) []trace.Span {
	return []trace.Span{
		mkSpan(tr, 1, 0, trace.KindAgent, named("agent.run"), session("s1"), user("u1")),
		mkSpan(tr, 2, 1, trace.KindLLM, usage(100, 40), priced(5000), model("gpt-a"), lasting(2*time.Second)),
		mkSpan(tr, 3, 1, trace.KindTool, failed()),
		mkSpan(tr, 4, 1, trace.KindLLM, usage(10, 4), model("mystery")),
	}
}

func TestStore_WriteBatch_InsertsAndAggregates(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	res := writeSpans(t, s, simpleTrace(1)...)
	assert.Equal(t, 4, res.Inserted)
	assert.Equal(t, 0, res.Duplicates)

	d, err := s.GetTrace(ctx, tid(1), false)
	require.NoError(t, err)
	sum := d.Summary
	assert.Equal(t, "agent.run", sum.RootName)
	assert.Equal(t, "svc", sum.Service)
	assert.Equal(t, 4, sum.SpanCount)
	assert.Equal(t, trace.Usage{Input: 110, Output: 44}, sum.Usage)
	assert.Equal(t, int64(5000), sum.CostNanos)
	assert.Equal(t, 1, sum.UnpricedSpans)
	assert.True(t, sum.HasError)
	assert.Equal(t, []string{"gpt-a", "mystery"}, sum.Models)
	assert.Equal(t, "s1", sum.SessionID)
	assert.Equal(t, "u1", sum.UserID)
	assert.Equal(t, mkSpan(1, 1, 0, "").StartNs, sum.StartNs)
	assert.Equal(t, mkSpan(1, 2, 0, "").StartNs+int64(2*time.Second), sum.EndNs)
	require.Len(t, d.Spans, 4)
	assert.Empty(t, d.Spans[0].ParentSpanID)
	assert.JSONEq(t, `{"k":"v"}`, string(d.Spans[0].Attributes))

	sess, err := s.GetSession(ctx, "s1", query.Page{})
	require.NoError(t, err)
	assert.Equal(t, 1, sess.Summary.TraceCount)
	assert.Equal(t, int64(5000), sess.Summary.CostNanos)
	assert.Equal(t, "u1", sess.Summary.UserID)

	hours, err := s.ReadRollups(ctx, rollup.ResolutionHour, rollup.Range{
		FromNs: anchor.UnixNano(),
		ToNs:   anchor.Add(time.Hour).UnixNano(),
	}, rollup.Filter{Kind: trace.KindLLM})
	require.NoError(t, err)
	require.Len(t, hours, 2)
	var total rollup.Aggregate
	for i := range hours {
		total.Merge(&hours[i].Aggregate)
	}
	assert.Equal(t, rollup.Measures{Count: 2, Usage: trace.Usage{Input: 110, Output: 44}, CostNanos: 5000, Unpriced: 1},
		total.Measures)
	assert.Equal(t, uint64(2), total.Histogram.Count())

	users, err := s.ReadUserRollups(ctx, rollup.ResolutionDay, rollup.Range{
		FromNs: anchor.UnixNano(),
		ToNs:   anchor.Add(24 * time.Hour).UnixNano(),
	})
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "u1", users[0].Key.UserID, "spans without a user take the trace's user")
	assert.Equal(t, int64(4), users[0].Aggregate.Measures.Count)
	assert.Equal(t, int64(1), users[0].Aggregate.Measures.Errors)
}

func TestStore_WriteBatch_ResendChangesNothing(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	writeSpans(t, s, simpleTrace(1)...)
	before := snapshot(t, s.Path())

	again := simpleTrace(1)
	res := writeSpans(t, s, again[2], again[0], again[0])
	assert.Equal(t, 0, res.Inserted)
	assert.Equal(t, 3, res.Duplicates)
	assert.Equal(t, before, snapshot(t, s.Path()))

	st, err := s.IngestTotals(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(4), st.Stored)
	assert.Equal(t, int64(3), st.Duplicates)
}

func TestStore_WriteBatch_DuplicateWithinBatch(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	sp := mkSpan(1, 1, 0, trace.KindLLM, usage(3, 4), priced(9))
	res := writeSpans(t, s, sp, sp)
	assert.Equal(t, 1, res.Inserted)
	assert.Equal(t, 1, res.Duplicates)
	d, err := s.GetTrace(context.Background(), tid(1), false)
	require.NoError(t, err)
	assert.Equal(t, 1, d.Summary.SpanCount)
	assert.Equal(t, int64(9), d.Summary.CostNanos)
}

func TestStore_WriteBatch_NilAndCountersOnly(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	res, err := s.WriteBatch(ctx, nil)
	require.NoError(t, err)
	assert.Zero(t, res.Inserted)

	h := anchor.Add(90 * time.Minute).UnixNano()
	_, err = s.WriteBatch(ctx, &trace.Batch{Counts: []trace.IngestCount{
		{AtNs: h, Signal: trace.SignalTraces, Result: trace.ResultReceived, N: 5},
		{AtNs: h + 1, Signal: trace.SignalTraces, Result: trace.ResultReceived, N: 2},
		{AtNs: h, Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonQueueFull, N: 3},
		{AtNs: h, Signal: trace.SignalLogs, Result: trace.ResultRejected, Reason: trace.ReasonQueueFull, N: 50},
		{AtNs: h, Signal: trace.SignalTraces, Result: trace.ResultDropped, Reason: trace.ReasonStoreError, N: 4},
		{AtNs: h, Signal: trace.SignalLogs, Result: trace.ResultDiscarded, Reason: trace.ReasonNotGenAI, N: 6},
		{AtNs: h, Signal: trace.SignalMetrics, Result: trace.ResultDiscarded, Reason: trace.ReasonNotStored, N: 7},
		{AtNs: h, Signal: trace.SignalTraces, Result: trace.ResultContentStripped, N: 8},
		{AtNs: h, Signal: trace.SignalTraces, Result: trace.ResultUnpriced, N: 9},
		{AtNs: h, Signal: trace.SignalTraces, Result: trace.ResultUnpriced, N: 0},
	}})
	require.NoError(t, err)
	st, err := s.IngestTotals(ctx)
	require.NoError(t, err)
	assert.Equal(t, query.IngestStats{
		Received: 7, Rejected: map[string]int64{trace.ReasonQueueFull: 3},
		Dropped:   map[string]int64{trace.ReasonStoreError: 4},
		Discarded: map[string]int64{"logs": 6, "metrics": 7}, ContentStripped: 8, Unpriced: 9,
	}, st)
	db := rawDB(t, s.Path())
	assert.Equal(t, int64(1), queryInt(t, db,
		"SELECT count(*) FROM ingest_stats WHERE result = 'received' AND hour_ns = ?",
		rollup.HourStart(h)), "counters fold per UTC hour")
}

func TestStore_WriteBatch_CancelledContextStoresNothing(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.WriteBatch(ctx, &trace.Batch{Spans: simpleTrace(1)})
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.GetTrace(context.Background(), tid(1), false)
	require.ErrorIs(t, err, trace.ErrNotFound)
}

func TestStore_WriteBatch_Content(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	llm := mkSpan(1, 2, 1, trace.KindLLM, usage(1, 1))
	secret := bytes.Repeat([]byte("hello prompt "), 40)
	llm.Content = []trace.ContentPart{
		{
			Part: 0, Source: trace.SourceAttribute, Key: "gen_ai.input.messages", Role: "user",
			Bytes: len(secret), Body: secret,
		},
		{
			Part: 0, Source: trace.SourceEvent, Key: "gen_ai.choice", Role: "assistant", Bytes: 2, Body: []byte("ok"),
			TimeNs: llm.EndNs,
		},
	}
	stripped := mkSpan(1, 3, 1, trace.KindLLM)
	stripped.Content = []trace.ContentPart{{Source: trace.SourceAttribute, Key: "input.value", Role: "user", Bytes: 99}}
	logPart := trace.ContentPart{
		TraceID: tid(1), SpanID: sid(2), Part: 0, Source: trace.SourceLog, Key: "gen_ai.user.message",
		Role: "user", Bytes: 3, Body: []byte("log"), TimeNs: llm.StartNs + 5,
	}
	orphan := logPart
	orphan.SpanID = sid(9)

	_, err := s.WriteBatch(ctx, &trace.Batch{Content: []trace.ContentPart{logPart, orphan}})
	require.NoError(t, err)
	db := rawDB(t, s.Path())
	assert.Equal(t, int64(0), queryInt(t, db, "SELECT COALESCE(SUM(has_span), 0) FROM span_content"))
	writeSpans(t, s, mkSpan(1, 1, 0, trace.KindAgent), llm, stripped)
	assert.Equal(t, int64(1), queryInt(t, db, "SELECT count(*) FROM span_content WHERE has_span = 0"),
		"only the orphan stays unattached")
	var blob []byte
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT body FROM span_content WHERE key = 'gen_ai.input.messages'").Scan(&blob))
	assert.True(t, bytes.HasPrefix(blob, []byte{0x28, 0xb5, 0x2f, 0xfd}), "bodies are stored as zstd frames")
	assert.Less(t, len(blob), len(secret))

	d, err := s.GetTrace(ctx, tid(1), true)
	require.NoError(t, err)
	require.Len(t, d.Spans, 3)
	parts := d.Spans[1].Content
	require.Len(t, parts, 3)
	assert.Equal(t, string(secret), *parts[0].Body, "attribute parts are aged with the span start")
	assert.Equal(t, "log", *parts[1].Body)
	assert.Equal(t, "ok", *parts[2].Body)
	assert.False(t, d.Spans[1].ContentStripped)
	require.Len(t, d.Spans[2].Content, 1)
	assert.Nil(t, d.Spans[2].Content[0].Body)
	assert.Equal(t, 99, d.Spans[2].Content[0].Bytes)
	assert.True(t, d.Spans[2].ContentStripped, "a marker means content arrived and was stripped")

	noBodies, err := s.GetTrace(ctx, tid(1), false)
	require.NoError(t, err)
	for _, p := range noBodies.Spans[1].Content {
		assert.Nil(t, p.Body)
		assert.Positive(t, p.Bytes)
	}
}

func TestStore_WriteBatch_ContentPartsDoNotCollide(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	sp := mkSpan(1, 1, 0, trace.KindLLM)
	sp.Content = []trace.ContentPart{
		{Part: 0, Source: trace.SourceAttribute, Key: "gen_ai.input.messages", Bytes: 1, Body: []byte("a")},
		{Part: 0, Source: trace.SourceEvent, Key: "gen_ai.user.message", Bytes: 1, Body: []byte("e"), TimeNs: sp.StartNs},
	}
	logs := []trace.ContentPart{
		{
			TraceID: tid(1), SpanID: sid(1), Source: trace.SourceLog, Key: "gen_ai.user.message", Bytes: 1,
			Body: []byte("l1"), TimeNs: sp.StartNs,
		},
		{
			TraceID: tid(1), SpanID: sid(1), Source: trace.SourceLog, Key: "gen_ai.choice", Bytes: 1,
			Body: []byte("l2"), TimeNs: sp.StartNs,
		},
		{
			TraceID: tid(1), SpanID: sid(1), Source: trace.SourceLog, Key: "gen_ai.user.message", Bytes: 1,
			Body: []byte("l3"), TimeNs: sp.StartNs + 1,
		},
	}
	_, err := s.WriteBatch(context.Background(), &trace.Batch{Spans: []trace.Span{sp}, Content: logs})
	require.NoError(t, err)
	_, err = s.WriteBatch(context.Background(), &trace.Batch{Content: logs})
	require.NoError(t, err)
	db := rawDB(t, s.Path())
	assert.Equal(t, int64(5), queryInt(t, db, "SELECT count(*) FROM span_content"),
		"distinct carriers are kept, re-sent log events are ignored")
}

func TestStore_IngestTotalsBetween(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	h := anchor.Truncate(time.Hour)
	at := func(d time.Duration) int64 { return h.Add(d).UnixNano() }
	rej := func(d time.Duration, n int64) trace.IngestCount {
		return trace.IngestCount{
			AtNs: at(d), Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonQueueFull, N: n,
		}
	}
	_, err := s.WriteBatch(ctx, &trace.Batch{Counts: []trace.IngestCount{
		rej(-30*time.Minute, 1), rej(10*time.Minute, 2), rej(70*time.Minute, 4), rej(130*time.Minute, 8),
		{
			AtNs: at(70 * time.Minute), Signal: trace.SignalTraces, Result: trace.ResultDropped,
			Reason: trace.ReasonStoreError, N: 16,
		},
		{AtNs: at(70 * time.Minute), Signal: trace.SignalTraces, Result: trace.ResultReceived, N: 32},
	}})
	require.NoError(t, err)

	st, err := s.IngestTotalsBetween(ctx, h.Add(30*time.Minute), h.Add(90*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{trace.ReasonQueueFull: 6}, st.Rejected)
	assert.Equal(t, map[string]int64{trace.ReasonStoreError: 16}, st.Dropped)
	assert.Equal(t, int64(32), st.Received)

	st, err = s.IngestTotalsBetween(ctx, h, h.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{trace.ReasonQueueFull: 2}, st.Rejected)
	assert.Empty(t, st.Dropped)

	all, err := s.IngestTotals(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(15), all.Rejected[trace.ReasonQueueFull])

	_, err = s.IngestTotalsBetween(ctx, h, h)
	require.ErrorIs(t, err, query.ErrInvalidRange)
}
