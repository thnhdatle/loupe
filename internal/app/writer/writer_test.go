// SPDX-License-Identifier: Apache-2.0

package writer_test

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/testutil"
	"github.com/thnhdatle/loupe/internal/testutil/fakespanstore"
)

var errDisk = errors.New("disk I/O error")

func TestNew_RejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	store, clk := fakespanstore.New(), clock.NewFake(epoch)
	tests := []struct {
		name string
		opt  writer.Option
	}{
		{name: "batch spans", opt: writer.WithBatchSpans(0)},
		{name: "batch interval", opt: writer.WithBatchInterval(0)},
		{name: "queue spans", opt: writer.WithQueueLimits(0, 1)},
		{name: "queue bytes", opt: writer.WithQueueLimits(1, 0)},
		{name: "shutdown timeout", opt: writer.WithShutdownTimeout(0)},
		{name: "write timeout", opt: writer.WithWriteTimeout(-time.Second)},
		{name: "retry attempts", opt: writer.WithRetry(0, time.Millisecond, time.Second)},
		{name: "retry max below base", opt: writer.WithRetry(3, time.Second, time.Millisecond)},
		{name: "job queue", opt: writer.WithJobQueue(0)},
		{name: "nil logger", opt: writer.WithLogger(nil)},
		{name: "nil rand", opt: writer.WithRand(nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := writer.New(store, clk, tc.opt)
			require.ErrorIs(t, err, writer.ErrInvalidConfig)
		})
	}
	t.Run("nil store", func(t *testing.T) {
		t.Parallel()
		_, err := writer.New(nil, clk)
		require.ErrorIs(t, err, writer.ErrInvalidConfig)
	})
	t.Run("nil clock", func(t *testing.T) {
		t.Parallel()
		_, err := writer.New(store, nil)
		require.ErrorIs(t, err, writer.ErrInvalidConfig)
	})
}

func TestWriter_Enqueue_RejectsInvalidItem(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tests := []struct {
		name  string
		b     *trace.Batch
		slots int
		bytes int64
	}{
		{name: "nil batch", b: nil, slots: 1},
		{name: "no slots", b: spans(1), slots: 0},
		{name: "negative bytes", b: spans(1), slots: 1, bytes: -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := f.w.Enqueue(tc.b, tc.slots, tc.bytes, false)
			require.ErrorIs(t, err, writer.ErrInvalidItem)
		})
	}
}

func TestWriter_Run_FlushBySize(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithBatchInterval(time.Hour))
	f.run()
	// Three items stay below batch_spans = 4: nothing is written until the
	// fourth arrives, and then all four go in one batch without any tick.
	for range 3 {
		f.enqueue(1, false)
	}
	f.enqueue(1, false)
	testutil.Eventually(t, func() bool { return len(f.store.Batches()) == 1 }, wait)
	assert.Len(t, f.store.Batches()[0].Spans, 4)
	assert.Equal(t, 1, f.store.Calls())
}

func TestWriter_Run_SplitsBatchesAtWholeItems(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithBatchInterval(time.Hour))
	// Queued before Run: 3 + 3 + 1 + 6 slots with batch_spans 4 form batches
	// of whole items: [3], [3 1], [6].
	for _, n := range []int{3, 3, 1, 6} {
		f.enqueue(n, false)
	}
	f.run()
	testutil.Eventually(t, func() bool { return len(f.store.Batches()) == 3 }, wait)
	bs := f.store.Batches()
	assert.Len(t, bs[0].Spans, 3)
	assert.Len(t, bs[1].Spans, 4)
	assert.Len(t, bs[2].Spans, 6)
	assert.Len(t, bs[1].Resources, 1, "resources are deduplicated by id within a batch")
}

func TestWriter_Run_FlushByTime(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithBatchSpans(100))
	f.run()
	f.enqueue(2, false)
	f.advanceUntil(interval, func() bool { return len(f.store.Batches()) == 1 })
	assert.Len(t, f.store.Batches()[0].Spans, 2)
}

func TestWriter_Run_FlushOnShutdown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithBatchSpans(100), writer.WithBatchInterval(time.Hour))
	stop := f.run()
	acks := []<-chan error{f.enqueue(2, true), f.enqueue(3, true)}
	require.NoError(t, stop())
	assert.Equal(t, 5, spanTotal(f.store.Batches()))
	for _, ack := range acks {
		require.NoError(t, receive(t, ack))
	}
	_, err := f.w.Enqueue(spans(1), 1, 1, false)
	require.ErrorIs(t, err, trace.ErrShuttingDown)
	assert.Zero(t, f.w.Stats().QueuedSlots)
}

func TestWriter_Run_StoreErrorRetriesThenCommits(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.FailNext(errDisk, errDisk)
	f.run()
	ack := f.enqueue(4, true)
	f.advanceUntil(10*time.Millisecond, func() bool { return len(f.store.Batches()) == 1 })
	require.NoError(t, receive(t, ack))
	st := f.w.Stats()
	assert.Equal(t, int64(2), st.Retries)
	assert.Equal(t, 3, f.store.Calls())
	assert.Equal(t, int64(4), st.Committed)
	assert.Equal(t, int64(4), st.Inserted)
	assert.Zero(t, st.DroppedStoreError)
}

func TestWriter_Run_StoreErrorDropsAndCountsAfterRetries(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.FailAlways(errDisk)
	f.run()
	ack := f.enqueue(4, true)
	var got error
	f.advanceUntil(10*time.Millisecond, func() bool {
		select {
		case got = <-ack:
			return true
		default:
			return false
		}
	})
	require.ErrorIs(t, got, errDisk)
	require.NotErrorIs(t, got, trace.ErrShuttingDown)
	st := f.w.Stats()
	assert.Equal(t, 3, f.store.Calls(), "three attempts")
	assert.Equal(t, int64(2), st.Retries)
	assert.Equal(t, int64(4), st.DroppedStoreError)
	assert.Zero(t, st.QueuedSlots, "slots are released at the final drop")
	assert.Zero(t, st.QueuedBytes)

	// Once the store recovers, the drop is stored in ingest_stats with the next batch.
	f.store.FailAlways(nil)
	f.enqueue(4, false)
	testutil.Eventually(t, func() bool { return len(f.store.Batches()) == 1 }, wait)
	counts := f.store.Counts()
	assert.Equal(t, int64(4), counts[fakespanstore.CountKey{
		Signal: trace.SignalTraces, Result: trace.ResultDropped, Reason: trace.ReasonStoreError,
	}])
}

func TestWriter_Run_DroppedLogSlotsCountedAsLogs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.FailAlways(errDisk)
	f.run()
	logs := &trace.Batch{Content: []trace.ContentPart{{Key: "gen_ai.user.message"}, {Key: "gen_ai.choice"}}}
	_, err := f.w.Enqueue(logs, 2, 300, false)
	require.NoError(t, err)
	f.enqueue(2, false)
	f.advanceUntil(10*time.Millisecond, func() bool { return f.w.Stats().DroppedStoreError == 4 })
	f.store.FailAlways(nil)
	f.advanceUntil(interval, func() bool { return len(f.store.Batches()) == 1 })
	counts := f.store.Counts()
	assert.Equal(t, int64(2), counts[fakespanstore.CountKey{
		Signal: trace.SignalLogs, Result: trace.ResultDropped, Reason: trace.ReasonStoreError,
	}])
	assert.Equal(t, int64(2), counts[fakespanstore.CountKey{
		Signal: trace.SignalTraces, Result: trace.ResultDropped, Reason: trace.ReasonStoreError,
	}])
}

func TestWriter_Run_SlotsReleasedOnlyAtCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithQueueLimits(6, 1<<20))
	release := f.store.Hold()
	f.run()
	ack := f.enqueue(4, true) // forms a full batch: the write starts and blocks
	testutil.Eventually(t, func() bool { return f.store.Calls() == 1 }, wait)

	st := f.w.Stats()
	assert.Equal(t, 4, st.QueuedSlots, "a batch being written still holds its slots")
	assert.Equal(t, int64(400), st.QueuedBytes)
	f.enqueue(2, false)
	_, err := f.w.Enqueue(spans(1), 1, 1, false)
	require.ErrorIs(t, err, trace.ErrOverloaded, "4 in flight + 2 queued fill queue_spans = 6")

	release()
	require.NoError(t, receive(t, ack))
	f.advanceUntil(interval, func() bool { return f.w.Stats().QueuedSlots == 0 })
	assert.Equal(t, 6, spanTotal(f.store.Batches()))
}

func TestWriter_Run_SlotsHeldDuringRetryBackoff(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.FailNext(errDisk)
	f.run()
	f.enqueue(4, false)
	testutil.Eventually(t, func() bool { return f.w.Stats().Retries == 1 }, wait)
	assert.Equal(t, 4, f.w.Stats().QueuedSlots, "a failed batch keeps its slots while it waits to retry")
	f.advanceUntil(10*time.Millisecond, func() bool { return f.w.Stats().QueuedSlots == 0 })
	assert.Equal(t, int64(4), f.w.Stats().Committed)
}

func TestWriter_Enqueue_QueueLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		spans  int
		bytes  int64
		first  [2]int64 // slots, bytes of the item already queued
		second [2]int64
		want   error
		// room is HasRoom(second slots) before the second Enqueue: it ignores
		// the item's bytes.
		room bool
	}{
		{name: "fits", spans: 10, bytes: 1000, first: [2]int64{5, 500}, second: [2]int64{5, 500}, room: true},
		{
			name: "span limit", spans: 10, bytes: 1000,
			first: [2]int64{5, 10}, second: [2]int64{6, 10}, want: trace.ErrOverloaded,
		},
		{
			name: "byte limit", spans: 10, bytes: 1000,
			first: [2]int64{1, 900}, second: [2]int64{1, 101}, want: trace.ErrOverloaded, room: true,
		},
		{
			name: "oversized item admitted alone", spans: 10, bytes: 1000,
			first: [2]int64{50, 5000}, second: [2]int64{1, 1}, want: trace.ErrOverloaded,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, writer.WithQueueLimits(tc.spans, tc.bytes), writer.WithBatchSpans(1000))
			assert.True(t, f.w.HasRoom(int(tc.first[0])), "an empty queue always has room")
			_, err := f.w.Enqueue(spans(int(tc.first[0])), int(tc.first[0]), tc.first[1], false)
			require.NoError(t, err)
			assert.Equal(t, tc.room, f.w.HasRoom(int(tc.second[0])))
			_, err = f.w.Enqueue(spans(int(tc.second[0])), int(tc.second[0]), tc.second[1], false)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestWriter_Run_SyncAckWaitsForCommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithBatchSpans(100))
	release := f.store.Hold()
	f.run()
	ack := f.enqueue(2, true)
	noAck := f.enqueue(1, false)
	assert.Nil(t, noAck)
	f.advanceUntil(interval, func() bool { return f.store.Calls() == 1 })
	select {
	case <-ack:
		t.Fatal("ack before commit")
	default:
	}
	release()
	require.NoError(t, receive(t, ack))
	assert.Equal(t, 3, f.store.SpanCount())
}

func TestWriter_Run_ShutdownDeadlineAbandonsQueue(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithShutdownTimeout(5*time.Second), writer.WithBatchSpans(2))
	f.store.Hold() // never released: the store hangs
	stop := f.run()
	inflight := f.enqueue(2, true)
	testutil.Eventually(t, func() bool { return f.store.Calls() == 1 }, wait)
	queued := f.enqueue(3, true)

	errc := make(chan error, 1)
	go func() { errc <- stop() }()
	var runErr error
	f.advanceUntil(time.Second, func() bool {
		select {
		case runErr = <-errc:
			return true
		default:
			return false
		}
	})
	require.ErrorIs(t, runErr, writer.ErrDrainIncomplete)
	require.ErrorIs(t, receive(t, inflight), trace.ErrShuttingDown)
	require.ErrorIs(t, receive(t, queued), trace.ErrShuttingDown)
	st := f.w.Stats()
	assert.Equal(t, int64(5), st.DroppedShutdown)
	assert.Zero(t, st.QueuedSlots)
	assert.Zero(t, f.store.SpanCount())
}

func TestWriter_Run_RetryWaitEndsAtShutdownDeadline(t *testing.T) {
	t.Parallel()
	f := newFixture(t,
		writer.WithRetry(5, time.Hour, time.Hour), writer.WithShutdownTimeout(time.Second))
	f.store.FailAlways(errDisk)
	var (
		mu       sync.Mutex
		attempts []int // spans per WriteBatch call
	)
	f.store.OnWrite(func(b *trace.Batch) {
		mu.Lock()
		attempts = append(attempts, len(b.Spans))
		mu.Unlock()
	})
	stop := f.run()
	ack := f.enqueue(4, true)
	testutil.Eventually(t, func() bool { return f.store.Calls() == 1 }, wait)
	errc := make(chan error, 1)
	go func() { errc <- stop() }()
	var runErr error
	f.advanceUntil(100*time.Millisecond, func() bool {
		select {
		case runErr = <-errc:
			return true
		default:
			return false
		}
	})
	require.ErrorIs(t, runErr, writer.ErrDrainIncomplete)
	require.ErrorIs(t, receive(t, ack), trace.ErrShuttingDown)
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, attempts)
	assert.Equal(t, 4, attempts[0])
	assert.False(t, slices.ContainsFunc(attempts[1:], func(n int) bool { return n > 0 }),
		"no retry of the batch after the deadline, only the final counters write: %v", attempts)
}

// Items abandoned at the drain deadline are counted dropped{shutdown} in
// ingest_stats by a final counters write within the shutdown timeout.
func TestWriter_Run_ShutdownDeadlineCountsAbandoned(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithShutdownTimeout(5*time.Second), writer.WithBatchSpans(2))
	release := f.store.Hold() // the in-flight batch hangs until the drain deadline
	f.store.OnWrite(func(b *trace.Batch) {
		if len(b.Spans) == 0 {
			release() // the final counters write goes through
		}
	})
	stop := f.run()
	f.enqueue(2, false)
	testutil.Eventually(t, func() bool { return f.store.Calls() == 1 }, wait)
	f.enqueue(3, false)

	errc := make(chan error, 1)
	go func() { errc <- stop() }()
	var runErr error
	f.advanceUntil(100*time.Millisecond, func() bool {
		select {
		case runErr = <-errc:
			return true
		default:
			return false
		}
	})
	require.ErrorIs(t, runErr, writer.ErrDrainIncomplete)
	assert.Equal(t, int64(5), f.w.Stats().DroppedShutdown)
	assert.Equal(t, int64(5), f.store.Counts()[fakespanstore.CountKey{
		Signal: trace.SignalTraces, Result: trace.ResultDropped, Reason: trace.ReasonShutdown,
	}])
	assert.Zero(t, f.store.SpanCount())
}

func TestWriter_Run_WriteTimeout(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithWriteTimeout(time.Second))
	release := f.store.Hold() // the first attempt hangs until its timeout
	f.run()
	ack := f.enqueue(4, true)
	testutil.Eventually(t, func() bool { return f.store.Calls() == 1 }, wait)
	release()
	f.advanceUntil(100*time.Millisecond, func() bool { return len(f.store.Batches()) == 1 })
	require.NoError(t, receive(t, ack))
}

func TestWriter_Run_TwiceFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.run()
	f.enqueue(4, false)
	testutil.Eventually(t, func() bool { return f.store.Calls() == 1 }, wait) // the first Run is active
	require.ErrorIs(t, f.w.Run(t.Context()), writer.ErrAlreadyRunning)
}

func TestWriter_AddCount_FlushedWithoutSpans(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	at := epoch.UnixNano()
	f.w.AddCount(trace.IngestCount{
		AtNs: at, Signal: trace.SignalMetrics, Result: trace.ResultDiscarded,
		Reason: trace.ReasonNotStored, N: 3,
	})
	f.w.AddCount(trace.IngestCount{
		AtNs: at + int64(time.Minute), Signal: trace.SignalMetrics,
		Result: trace.ResultDiscarded, Reason: trace.ReasonNotStored, N: 2,
	})
	f.w.AddCount(trace.IngestCount{AtNs: at, Signal: trace.SignalTraces, Result: trace.ResultRejected, N: 0})
	f.run()
	f.advanceUntil(interval, func() bool { return len(f.store.Batches()) == 1 })
	b := f.store.Batches()[0]
	require.Len(t, b.Counts, 1, "same hour, signal, result and reason fold into one counter; N=0 is ignored")
	hour := epoch.Truncate(time.Hour).UnixNano()
	assert.Equal(t, trace.IngestCount{
		AtNs: hour, Signal: trace.SignalMetrics, Result: trace.ResultDiscarded, Reason: trace.ReasonNotStored, N: 5,
	}, b.Counts[0])
	assert.Empty(t, b.Spans)
}

func TestWriter_AddCount_SurvivesFailedBatch(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithRetry(1, time.Millisecond, time.Millisecond))
	f.store.FailNext(errDisk)
	f.w.AddCount(trace.IngestCount{AtNs: 1, Signal: trace.SignalTraces, Result: trace.ResultReceived, N: 9})
	f.run()
	f.advanceUntil(interval, func() bool { return len(f.store.Batches()) == 1 })
	assert.Equal(t, int64(9), f.store.Counts()[fakespanstore.CountKey{
		Signal: trace.SignalTraces, Result: trace.ResultReceived,
	}])
}

func TestWriter_Run_DrainStoresCountersOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.w.AddCount(trace.IngestCount{AtNs: 1, Signal: trace.SignalTraces, Result: trace.ResultReceived, N: 1})
	stop := f.run()
	require.NoError(t, stop())
	assert.Len(t, f.store.Batches(), 1)
}
