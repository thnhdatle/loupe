// SPDX-License-Identifier: Apache-2.0

package ingest_test

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/testutil"
)

func TestParseOverflow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want ingest.Overflow
		err  bool
	}{
		{name: "reject", in: "reject", want: ingest.OverflowReject},
		{name: "drop", in: "drop", want: ingest.OverflowDrop},
		{name: "unknown", in: "block", err: true},
		{name: "empty", in: "", err: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ingest.ParseOverflow(tc.in)
			if tc.err {
				require.ErrorIs(t, err, ingest.ErrInvalidOverflow)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(epoch)
	w, err := writer.New(&nopStore{}, clk)
	require.NoError(t, err)
	calc := calculator(t)
	tests := []struct {
		name string
		q    ingest.Queue
		opts []ingest.Option
		want error
	}{
		{name: "nil queue", want: ingest.ErrInvalidConfig},
		{name: "overflow", q: w, opts: []ingest.Option{ingest.WithOverflow("block")}, want: ingest.ErrInvalidOverflow},
		{
			name: "capture mode", q: w, want: ingest.ErrInvalidConfig,
			opts: []ingest.Option{ingest.WithContentPolicy(content.Policy{Mode: "full"})},
		},
		{
			name: "attribute bytes", q: w, want: ingest.ErrInvalidConfig,
			opts: []ingest.Option{ingest.WithMaxAttributeBytes(-1)},
		},
		{name: "logger", q: w, opts: []ingest.Option{ingest.WithLogger(nil)}, want: ingest.ErrInvalidConfig},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ingest.New(tc.q, calc, clk, tc.opts...)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestIngestor_IngestSpans_TranslatesPricesAndStores(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.run()
	sameAttrs := &trace.RawResource{Attributes: slices.Clone(res.Attributes)}
	other := chatSpan()
	other.Resource = sameAttrs
	raws := []trace.RawSpan{
		chatSpan(sattr("gen_ai.input.messages", `[{"role":"user","content":"hi"}]`)),
		other,
		chatSpan(sattr("gen_ai.request.model", "unknown-model")),
	}
	res, err := f.in.IngestSpans(t.Context(), raws)
	require.NoError(t, err)
	assert.Equal(t, trace.IngestResult{Accepted: 3}, res)
	f.flushUntil(func() bool { return f.store.SpanCount() == 3 })

	s, ok := f.store.Span(raws[0].TraceID, raws[0].SpanID)
	require.True(t, ok)
	assert.Equal(t, trace.KindLLM, s.Kind)
	assert.Equal(t, "checkout", s.Service)
	assert.Equal(t, trace.Priced(wantNanos), s.Cost)
	assert.True(t, s.ContentStripped, "capture is off by default")
	require.Len(t, s.Content, 1)
	assert.True(t, s.Content[0].IsMarker())
	assert.NotContains(t, string(s.Attributes), "hi")

	unpriced, _ := f.store.Span(raws[2].TraceID, raws[2].SpanID)
	assert.False(t, unpriced.Cost.Priced)

	b := f.store.Batches()[0]
	require.Len(t, b.Resources, 1, "one resource per distinct set")
	assert.Equal(t, s.ResourceID, b.Resources[0].ID)

	f.flushUntil(func() bool { return f.count(trace.SignalTraces, trace.ResultReceived, "") == 3 })
	assert.Equal(t, int64(1), f.count(trace.SignalTraces, trace.ResultContentStripped, ""))
	assert.Equal(t, int64(1), f.count(trace.SignalTraces, trace.ResultUnpriced, ""))
}

func TestIngestor_IngestSpans_ContentPolicy(t *testing.T) {
	t.Parallel()
	redactor, err := content.NewRedactor([]string{"email"}, nil)
	require.NoError(t, err)
	tests := []struct {
		name     string
		policy   content.Policy
		stripped bool
		check    func(t *testing.T, s trace.Span)
	}{
		{
			name: "on with redaction", policy: content.Policy{Mode: content.ModeOn, MaxBytes: 1 << 10, Redactor: redactor},
			check: func(t *testing.T, s trace.Span) {
				t.Helper()
				require.Len(t, s.Content, 1)
				assert.NotContains(t, string(s.Content[0].Body), "ann@example.com")
				assert.Contains(t, string(s.Content[0].Body), "write to")
				assert.NotContains(t, string(s.Attributes), "bob@example.com", "non-content values are redacted too")
			},
		},
		{
			name: "on truncated", policy: content.Policy{Mode: content.ModeOn, MaxBytes: 4},
			check: func(t *testing.T, s trace.Span) {
				t.Helper()
				require.Len(t, s.Content, 1)
				assert.True(t, s.Content[0].Truncated)
				assert.Len(t, s.Content[0].Body, 4)
			},
		},
		{
			name: "metadata only", policy: content.Policy{Mode: content.ModeMetadataOnly}, stripped: true,
			check: func(t *testing.T, s trace.Span) {
				t.Helper()
				require.Len(t, s.Content, 1)
				assert.Nil(t, s.Content[0].Body)
				assert.Equal(t, "input", s.Content[0].Role)
				assert.Positive(t, s.Content[0].Bytes)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil, ingest.WithContentPolicy(tc.policy))
			f.run()
			raw := chatSpan(sattr("gen_ai.prompt", "write to ann@example.com"), sattr("app.owner", "bob@example.com"))
			_, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{raw})
			require.NoError(t, err)
			f.flushUntil(func() bool { return f.store.SpanCount() == 1 })
			s, _ := f.store.Span(raw.TraceID, raw.SpanID)
			assert.Equal(t, tc.stripped, s.ContentStripped)
			tc.check(t, s)
		})
	}
}

func TestIngestor_IngestSpans_AttributeLimitAndBadUsage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil, ingest.WithMaxAttributeBytes(16))
	f.run()
	long := chatSpan(sattr("app.note", strings.Repeat("x", 100)))
	negative := chatSpan(iattr("gen_ai.usage.input_tokens", math.MaxInt64/2))
	_, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{long, negative})
	require.NoError(t, err)
	f.flushUntil(func() bool { return f.store.SpanCount() == 2 })
	s, _ := f.store.Span(long.TraceID, long.SpanID)
	assert.True(t, s.Flags.Has(trace.FlagAttrTruncated))
	n, _ := f.store.Span(negative.TraceID, negative.SpanID)
	assert.False(t, n.Cost.Priced, "a cost error leaves the span unpriced instead of failing ingest")
}

func TestIngestor_IngestSpans_EmptyAndCancelled(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	res, err := f.in.IngestSpans(t.Context(), nil)
	require.NoError(t, err)
	assert.Zero(t, res)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = f.in.IngestSpans(ctx, []trace.RawSpan{chatSpan()})
	require.ErrorIs(t, err, context.Canceled)
	_, err = f.in.IngestLogs(ctx, []trace.RawLogRecord{{}})
	require.ErrorIs(t, err, context.Canceled)
	res, err = f.in.IngestLogs(t.Context(), nil)
	require.NoError(t, err)
	assert.Zero(t, res)
	assert.Zero(t, f.w.Stats().QueuedSlots)
}

func TestIngestor_IngestSpans_Overflow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		overflow ingest.Overflow
		wantErr  error
		want     trace.IngestResult
		result   trace.Result
	}{
		{
			name: "reject", overflow: ingest.OverflowReject, wantErr: trace.ErrOverloaded,
			want: trace.IngestResult{Rejected: 2, Message: "ingest queue full; retry later"}, result: trace.ResultRejected,
		},
		{
			name: "drop", overflow: ingest.OverflowDrop,
			want: trace.IngestResult{Dropped: 2, Message: "ingest queue full; spans dropped"}, result: trace.ResultDropped,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, []writer.Option{writer.WithQueueLimits(3, 1<<30)}, ingest.WithOverflow(tc.overflow))
			_, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan(), chatSpan()})
			require.NoError(t, err)
			res, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan(), chatSpan()})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, res)
			f.run()
			f.flushUntil(func() bool { return f.count(trace.SignalTraces, tc.result, trace.ReasonQueueFull) == 2 })
			assert.Equal(t, 2, f.store.SpanCount(), "a request is accepted or refused as a whole")
			assert.Equal(t, int64(4), f.count(trace.SignalTraces, trace.ResultReceived, ""))
		})
	}
}

func TestIngestor_IngestSpans_OverflowBeforeTranslation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []writer.Option{writer.WithQueueLimits(1, 1<<30)}, ingest.WithOverflow(ingest.OverflowDrop))
	_, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	require.NoError(t, err)
	unknown := chatSpan(sattr("gen_ai.request.model", "no-such-model"))
	res, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{unknown})
	require.NoError(t, err)
	assert.Equal(t, trace.IngestResult{Dropped: 1, Message: "ingest queue full; spans dropped"}, res)
	f.run()
	f.flushUntil(func() bool { return f.count(trace.SignalTraces, trace.ResultDropped, trace.ReasonQueueFull) == 1 })
	assert.Equal(t, int64(2), f.count(trace.SignalTraces, trace.ResultReceived, ""))
	assert.Zero(t, f.count(trace.SignalTraces, trace.ResultUnpriced, ""))
	assert.Equal(t, 1, f.store.SpanCount())
}

func TestIngestor_IngestSpans_ByteLimitDrop(t *testing.T) {
	t.Parallel()
	one := ingest.SpanOverheadBytes + 64
	f := newFixture(t, []writer.Option{writer.WithQueueLimits(1000, int64(one))}, ingest.WithOverflow(ingest.OverflowDrop))
	_, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	require.NoError(t, err)
	res, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan(), chatSpan()})
	require.NoError(t, err)
	assert.Equal(t, trace.IngestResult{Dropped: 2, Message: "ingest queue full; spans dropped"}, res)
	f.run()
	f.flushUntil(func() bool { return f.count(trace.SignalTraces, trace.ResultDropped, trace.ReasonQueueFull) == 2 })
}

func TestIngestor_IngestSpans_ByteLimit(t *testing.T) {
	t.Parallel()
	one := ingest.SpanOverheadBytes + 64
	f := newFixture(t, []writer.Option{writer.WithQueueLimits(1000, int64(one))})
	_, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	require.NoError(t, err, "an item is always admitted into an empty queue")
	_, err = f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	require.ErrorIs(t, err, trace.ErrOverloaded)
	assert.Greater(t, f.w.Stats().QueuedBytes, int64(ingest.SpanOverheadBytes))
}

func TestIngestor_IngestSpans_ShuttingDown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	stop := f.run()
	require.NoError(t, stop())
	res, err := f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	require.ErrorIs(t, err, trace.ErrShuttingDown)
	assert.Equal(t, 1, res.Rejected)
	res, err = f.in.IngestLogs(t.Context(), []trace.RawLogRecord{genAILog("gen_ai.user.message", "hi")})
	require.ErrorIs(t, err, trace.ErrShuttingDown)
	assert.Equal(t, 1, res.Rejected)
}

func TestIngestor_IngestSpans_SyncAckWaitsForCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil, ingest.WithSyncAck(true))
	release := f.store.Hold()
	f.run()
	done := async(func() (trace.IngestResult, error) {
		return f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan(), chatSpan()})
	})
	f.flushUntil(func() bool { return f.store.Calls() == 1 })
	select {
	case <-done:
		t.Fatal("sync_ack returned before the batch committed")
	default:
	}
	release()
	r := receive(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, trace.IngestResult{Accepted: 2}, r.res)
	assert.Equal(t, 2, f.store.SpanCount())
}

func TestIngestor_IngestSpans_SyncAckReportsStoreErrorDrop(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil, ingest.WithSyncAck(true))
	f.store.FailAlways(errors.New("disk full"))
	f.run()
	done := async(func() (trace.IngestResult, error) {
		return f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	})
	var r result
	f.flushUntil(func() bool {
		select {
		case r = <-done:
			return true
		default:
			return false
		}
	})
	require.NoError(t, r.err)
	assert.Equal(t, trace.IngestResult{Dropped: 1, Message: "store error; spans dropped"}, r.res)
}

func TestIngestor_IngestSpans_SyncAckAbandonedAtShutdown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil, ingest.WithSyncAck(true))
	f.store.Hold()
	stop := f.run()
	done := async(func() (trace.IngestResult, error) {
		return f.in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	})
	f.flushUntil(func() bool { return f.store.Calls() == 1 })
	errc := make(chan error, 1)
	go func() { errc <- stop() }()
	var r result
	f.flushUntil(func() bool {
		select {
		case r = <-done:
			return true
		default:
			return false
		}
	})
	require.ErrorIs(t, r.err, trace.ErrShuttingDown)
	assert.Equal(t, 1, r.res.Rejected)
	var runErr error
	f.flushUntil(func() bool {
		select {
		case runErr = <-errc:
			return true
		default:
			return false
		}
	})
	require.ErrorIs(t, runErr, writer.ErrDrainIncomplete)
}

func TestIngestor_IngestSpans_SyncAckContextCancelled(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil, ingest.WithSyncAck(true))
	ctx, cancel := context.WithCancel(t.Context())
	done := async(func() (trace.IngestResult, error) {
		return f.in.IngestSpans(ctx, []trace.RawSpan{chatSpan()})
	})
	testutil.Eventually(t, func() bool { return f.w.Stats().QueuedSlots == 1 }, wait)
	cancel()
	r := receive(t, done)
	require.ErrorIs(t, r.err, context.Canceled)
}

type errQueue struct{ err error }

func (q errQueue) Enqueue(*trace.Batch, int, int64, bool) (<-chan error, error) { return nil, q.err }

func (errQueue) AddCount(trace.IngestCount) {}

func (errQueue) HasRoom(int) bool { return true }

func TestIngestor_IngestSpans_UnexpectedQueueError(t *testing.T) {
	t.Parallel()
	errBoom := errors.New("boom")
	in, err := ingest.New(errQueue{err: errBoom}, calculator(t), clock.NewFake(epoch),
		ingest.WithLogger(slog.New(slog.DiscardHandler)))
	require.NoError(t, err)
	_, err = in.IngestSpans(t.Context(), []trace.RawSpan{chatSpan()})
	require.ErrorIs(t, err, errBoom)
}

func TestIngestor_Count(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.in.Count(trace.IngestCount{
		Signal: trace.SignalMetrics, Result: trace.ResultDiscarded,
		Reason: trace.ReasonNotStored, N: 7,
	})
	f.in.Count(trace.IngestCount{
		AtNs: epoch.Add(-48 * time.Hour).UnixNano(), Signal: trace.SignalTraces,
		Result: trace.ResultRejected, Reason: trace.ReasonTooManySpans, N: 2,
	})
	f.run()
	f.flushUntil(func() bool { return len(f.store.Batches()) == 1 })
	counts := f.store.Batches()[0].Counts
	require.Len(t, counts, 2)
	assert.Equal(t, epoch.Add(-48*time.Hour).UnixNano(), counts[0].AtNs, "an explicit time is kept")
	assert.Equal(t, epoch.UnixNano(), counts[1].AtNs, "a zero time means now")
	assert.Equal(t, int64(7), counts[1].N)
}

func TestIngestor_Concurrent(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []writer.Option{writer.WithBatchSpans(20), writer.WithQueueLimits(60, 1<<30)},
		ingest.WithOverflow(ingest.OverflowDrop))
	stop := f.run()
	const producers, requests = 6, 40
	var accepted, dropped [producers]int
	done := make(chan int, producers)
	for p := range producers {
		go func() {
			for range requests {
				res, err := f.in.IngestSpans(context.Background(), []trace.RawSpan{chatSpan(), chatSpan()})
				if !assert.NoError(t, err) {
					break
				}
				accepted[p] += res.Accepted
				dropped[p] += res.Dropped
			}
			done <- p
		}()
	}
	finished := 0
	f.flushUntil(func() bool {
		for {
			select {
			case <-done:
				finished++
				continue
			default:
			}
			return finished == producers
		}
	})
	require.NoError(t, stop())
	total, lost := 0, 0
	for p := range producers {
		total += accepted[p]
		lost += dropped[p]
	}
	assert.Equal(t, producers*requests*2, total+lost)
	assert.Equal(t, total, f.store.SpanCount(), "every accepted span is stored")
	assert.Equal(t, int64(lost), f.count(trace.SignalTraces, trace.ResultDropped, trace.ReasonQueueFull))
}

func TestIngestor_UnpricedHookCountsPerModel(t *testing.T) {
	t.Parallel()
	got := map[string]int64{}
	f := newFixture(t, nil, ingest.WithUnpricedHook(func(m string, n int64) { got[m] += n }))
	f.run()
	raws := []trace.RawSpan{
		chatSpan(sattr("gen_ai.request.model", "unknown-model")),
		chatSpan(sattr("gen_ai.request.model", "unknown-model")),
		chatSpan(),
	}
	_, err := f.in.IngestSpans(t.Context(), raws)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"unknown-model": 2}, got)
}
