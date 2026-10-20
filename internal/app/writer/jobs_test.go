// SPDX-License-Identifier: Apache-2.0

package writer_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/testutil"
)

var errJob = errors.New("job failed")

func job(name string, fn func(ctx context.Context) error) trace.Job {
	return trace.Job{Name: name, Run: fn}
}

func TestWriter_Submit_RunsJobAndReturnsItsError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		run     func(ctx context.Context) error
		wantErr error
		wantMsg string
	}{
		{name: "success", run: func(context.Context) error { return nil }},
		{name: "error is wrapped", run: func(context.Context) error { return errJob }, wantErr: errJob},
		{name: "panic becomes an error", run: func(context.Context) error { panic("boom") }, wantMsg: "panicked: boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.run()
			err := f.w.Submit(t.Context(), job("test_job", tc.run))
			switch {
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
			case tc.wantMsg != "":
				require.ErrorContains(t, err, tc.wantMsg)
			default:
				require.NoError(t, err)
			}
			// The writer survives a failing or panicking job.
			f.enqueue(4, false)
			testutil.Eventually(t, func() bool { return len(f.store.Batches()) == 1 }, wait)
			st := f.w.Stats()
			assert.Equal(t, int64(1), st.Jobs)
			assert.Equal(t, tc.wantErr != nil || tc.wantMsg != "", st.JobErrors == 1)
		})
	}
}

func TestWriter_Submit_RejectsInvalidJob(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tests := []struct {
		name string
		job  trace.Job
	}{
		{name: "no name", job: trace.Job{Run: func(context.Context) error { return nil }}},
		{name: "no run", job: trace.Job{Name: "x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, f.w.Submit(t.Context(), tc.job), writer.ErrInvalidJob)
		})
	}
}

func TestWriter_Submit_CancelledContext(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := f.w.Submit(ctx, job("x", func(context.Context) error { return nil }))
	require.ErrorIs(t, err, context.Canceled)
}

func TestWriter_Submit_QueuedJobSkippedWhenSubmitterGivesUp(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	release := f.store.Hold()
	f.run()
	f.enqueue(4, false) // the writer is now blocked in WriteBatch
	testutil.Eventually(t, func() bool { return f.store.Calls() == 1 }, wait)

	var ran atomic.Bool
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() {
		errc <- f.w.Submit(ctx, job("skipped", func(context.Context) error { ran.Store(true); return nil }))
	}()
	testutil.Eventually(t, func() bool { return f.w.Stats().QueuedJobs == 1 }, wait)
	cancel()
	require.ErrorIs(t, receive(t, errc), context.Canceled)
	release()
	// A later job runs after the abandoned one was dequeued and skipped.
	require.NoError(t, f.w.Submit(t.Context(), job("next", func(context.Context) error { return nil })))
	assert.False(t, ran.Load())
	assert.Equal(t, int64(1), f.w.Stats().Jobs)
}

func TestWriter_Submit_RunningJobCancelledWhenSubmitterGivesUp(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.run()
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() {
		errc <- f.w.Submit(ctx, job("long", func(jctx context.Context) error {
			close(started)
			<-jctx.Done()
			return jctx.Err()
		}))
	}()
	<-started
	cancel()
	require.ErrorIs(t, receive(t, errc), context.Canceled)
	testutil.Eventually(t, func() bool { return f.w.Stats().JobErrors == 1 }, wait)
}

func TestWriter_Submit_ShutdownFailsQueuedAndLateJobs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var ran atomic.Bool
	errc := make(chan error, 1)
	go func() {
		errc <- f.w.Submit(t.Context(), job("queued", func(context.Context) error { ran.Store(true); return nil }))
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, f.w.Run(ctx)) // shutdown wins over a ready job
	require.ErrorIs(t, receive(t, errc), trace.ErrShuttingDown)
	assert.False(t, ran.Load())
	err := f.w.Submit(t.Context(), job("late", func(context.Context) error { return nil }))
	require.ErrorIs(t, err, trace.ErrShuttingDown)
}

// TestWriter_Concurrent runs producers, job submitters and the writer together
// under -race and checks the single-writer invariants: every accepted span is
// committed exactly once, every slot is released, jobs and batch writes never
// overlap, and jobs are not starved by sustained ingest.
func TestWriter_Concurrent(t *testing.T) {
	t.Parallel()
	f := newFixture(t, writer.WithBatchSpans(16), writer.WithQueueLimits(200, 1<<30))
	var (
		active   atomic.Int32 // jobs plus writes in progress
		overlaps atomic.Int32
	)
	enter := func() {
		if active.Add(1) != 1 {
			overlaps.Add(1)
		}
	}
	f.store.OnWrite(func(*trace.Batch) { enter(); active.Add(-1) })
	stop := f.run()

	const producers, submitters, jobs = 8, 2, 15
	var accepted, rejected, jobsRun atomic.Int64
	var wg sync.WaitGroup
	for p := range producers {
		wg.Go(func() { produce(t, f.w, p, &accepted, &rejected) })
	}
	for range submitters {
		wg.Go(func() {
			for range jobs {
				assert.NoError(t, f.w.Submit(context.Background(), job("concurrent", func(context.Context) error {
					enter()
					defer active.Add(-1)
					jobsRun.Add(1)
					return nil
				})))
			}
		})
	}
	// Small batches wait for a tick; keep time moving until producers finish.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	f.advanceUntil(interval, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	require.NoError(t, stop())

	assert.Zero(t, overlaps.Load(), "a job overlapped a batch write or another job")
	assert.Equal(t, int64(submitters*jobs), jobsRun.Load())
	assert.Equal(t, int(accepted.Load()), f.store.SpanCount())
	st := f.w.Stats()
	assert.Equal(t, accepted.Load(), st.Committed)
	assert.Zero(t, st.QueuedSlots)
	assert.Zero(t, st.QueuedBytes)
	t.Logf("accepted=%d rejected=%d batches=%d", accepted.Load(), rejected.Load(), st.Batches)
}

// produce enqueues 60 items of 1 to 3 spans, waiting for every fifth to commit.
func produce(t *testing.T, w *writer.Writer, p int, accepted, rejected *atomic.Int64) {
	t.Helper()
	for i := range 60 {
		n := 1 + (p+i)%3
		ack, err := w.Enqueue(spans(n), n, int64(n)*64, i%5 == 0)
		if errors.Is(err, trace.ErrOverloaded) {
			rejected.Add(int64(n))
			continue
		}
		if err != nil {
			t.Errorf("enqueue: %v", err)
			return
		}
		accepted.Add(int64(n))
		if ack != nil {
			if err := <-ack; err != nil {
				t.Errorf("ack: %v", err)
			}
		}
	}
}
