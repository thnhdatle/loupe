// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Run is the writer goroutine. It flushes batches and runs jobs until ctx
// ends, then stops accepting work, drains the queue within the shutdown
// timeout and returns. It returns ErrDrainIncomplete (wrapped) when queued
// spans had to be abandoned, and ErrAlreadyRunning on a second call.
func (w *Writer) Run(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer close(w.stopped)
	// Store calls and jobs use wctx: it survives the end of ctx, so that an
	// in-flight batch and the drain can finish, and ends at the drain deadline
	// (shutdown_timeout minus the counter reserve). fctx ends at shutdown_timeout;
	// the final counters write uses it.
	wctx, fctx, stopDeadline := w.shutdownDeadline(ctx)
	defer stopDeadline()
	tick := w.clk.NewTicker(w.cfg.batchInterval)
	defer tick.Stop()
	for {
		// Shutdown takes priority over ready batches, ticks and jobs.
		if ctx.Err() != nil {
			return w.drain(wctx, fctx)
		}
		if w.batchReady() {
			w.flush(wctx)
			// Between size-triggered batches give one job a turn, so that jobs
			// do not starve under sustained ingest.
			select {
			case req := <-w.jobs:
				w.runJob(wctx, req)
			default:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return w.drain(wctx, fctx)
		case <-w.wake:
		case <-tick.C():
			w.flush(wctx)
		case req := <-w.jobs:
			w.runJob(wctx, req)
		}
	}
}

// counterReserve is the part of shutdown_timeout kept, after items are
// abandoned, for the final write of the pending counters (including the
// dropped{shutdown} counts of those items): a tenth of the timeout, at most 1 s.
func (w *Writer) counterReserve() time.Duration { return min(w.cfg.shutdownTimeout/10, time.Second) }

// shutdownDeadline returns two contexts that are not cancelled with ctx: wctx
// ends shutdown_timeout − counterReserve after ctx ends (the drain deadline),
// fctx at shutdown_timeout (on the injected clock). stop must be called.
func (w *Writer) shutdownDeadline(ctx context.Context) (wctx, fctx context.Context, stop func()) {
	fctx, cancelF := context.WithCancel(context.WithoutCancel(ctx))
	wctx, cancelW := context.WithCancel(fctx)
	quit := make(chan struct{})
	done := make(chan struct{})
	reserve := w.counterReserve()
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
		case <-quit:
			return
		}
		select {
		case <-w.clk.After(w.cfg.shutdownTimeout - reserve):
			cancelW()
		case <-quit:
			return
		}
		select {
		case <-w.clk.After(reserve):
			cancelF()
		case <-quit:
		}
	}()
	return wctx, fctx, func() {
		close(quit)
		<-done
		cancelW()
		cancelF()
	}
}

// withTimeout derives a context that is cancelled after d on the injected
// clock. cancel must be called; it also stops the helper goroutine.
func (w *Writer) withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	quit := make(chan struct{})
	done := make(chan struct{})
	timer := w.clk.After(d)
	go func() {
		defer close(done)
		select {
		case <-timer:
			cancel()
		case <-quit:
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		close(quit)
		<-done
		cancel()
	}
}

// flush writes the next batch (items and pending counters), if any.
func (w *Writer) flush(wctx context.Context) {
	entries, counts := w.take()
	if len(entries) == 0 && len(counts) == 0 {
		return
	}
	b := merge(entries, counts)
	start := w.clk.Now()
	res, err := w.writeWithRetry(wctx, b)
	switch {
	case err == nil:
		w.stats.recordCommit(entries, res)
		w.settle(entries, nil)
		w.log.Debug("batch committed", "count", len(b.Spans), "duration_ms", w.clk.Now().Sub(start).Milliseconds())
	case wctx.Err() != nil:
		// The drain deadline passed: the items cannot be written any more;
		// their counters go back to the pending set for the final write.
		w.restoreCounts(counts)
		w.abandon(entries)
	default:
		w.dropAfterRetries(entries, counts, err)
	}
}

// writeWithRetry calls WriteBatch up to retryAttempts times with jittered
// exponential backoff, stopping early when wctx ends.
func (w *Writer) writeWithRetry(wctx context.Context, b *trace.Batch) (trace.BatchResult, error) {
	var err error
	for attempt := 1; attempt <= w.cfg.retryAttempts; attempt++ {
		if attempt > 1 {
			w.stats.retries.Add(1)
			select {
			case <-w.clk.After(w.backoff(attempt - 1)):
			case <-wctx.Done():
				return trace.BatchResult{}, fmt.Errorf("writer: retry wait: %w", errors.Join(err, wctx.Err()))
			}
		}
		var res trace.BatchResult
		res, err = w.writeOnce(wctx, b)
		if err == nil {
			return res, nil
		}
		if wctx.Err() != nil {
			return trace.BatchResult{}, err
		}
		w.log.Warn("batch write failed", "attempt", attempt, "count", len(b.Spans), "error", err)
	}
	return trace.BatchResult{}, err
}

func (w *Writer) writeOnce(wctx context.Context, b *trace.Batch) (trace.BatchResult, error) {
	ctx, cancel := w.withTimeout(wctx, w.cfg.writeTimeout)
	defer cancel()
	res, err := w.store.WriteBatch(ctx, b)
	if err != nil {
		return trace.BatchResult{}, fmt.Errorf("writer: write batch: %w", err)
	}
	return res, nil
}

// backoff returns the delay before retry n (n >= 1): base·2^(n−1) capped at
// retryMax, with equal jitter.
func (w *Writer) backoff(n int) time.Duration {
	d := w.cfg.retryBase
	for i := 1; i < n && d < w.cfg.retryMax; i++ {
		d *= 2
	}
	d = min(d, w.cfg.retryMax)
	half := d / 2
	return half + time.Duration(w.cfg.rnd.Int64N(int64(d-half)+1))
}

// dropAfterRetries drops a batch whose retry budget is spent: its slots are
// released, waiters learn the error, and the spans are counted as dropped
// (store_error) in Stats and, with the next batch, in ingest_stats.
func (w *Writer) dropAfterRetries(entries []*entry, counts []trace.IngestCount, err error) {
	spans, logs := spanAndLogSlots(entries)
	w.stats.droppedStoreError.Add(spans + logs)
	now := w.clk.Now().UnixNano()
	w.restoreCounts(counts)
	w.AddCount(trace.IngestCount{
		AtNs: now, Signal: trace.SignalTraces, Result: trace.ResultDropped, Reason: trace.ReasonStoreError, N: spans,
	})
	w.AddCount(trace.IngestCount{
		AtNs: now, Signal: trace.SignalLogs, Result: trace.ResultDropped, Reason: trace.ReasonStoreError, N: logs,
	})
	w.log.Error("batch dropped after retries", "attempt", w.cfg.retryAttempts, "count", spans+logs, "error", err)
	w.settle(entries, fmt.Errorf("writer: batch dropped after %d attempts: %w", w.cfg.retryAttempts, err))
}

// abandon gives up on entries at the shutdown deadline: their waiters get
// ErrShuttingDown and their spans and log records are counted as
// dropped{shutdown} in Stats and in the pending counters, which drain writes
// once more before Run returns.
func (w *Writer) abandon(entries []*entry) {
	if len(entries) == 0 {
		return
	}
	spans, logs := spanAndLogSlots(entries)
	w.stats.droppedShutdown.Add(spans + logs)
	now := w.clk.Now().UnixNano()
	w.AddCount(trace.IngestCount{
		AtNs: now, Signal: trace.SignalTraces, Result: trace.ResultDropped, Reason: trace.ReasonShutdown, N: spans,
	})
	w.AddCount(trace.IngestCount{
		AtNs: now, Signal: trace.SignalLogs, Result: trace.ResultDropped, Reason: trace.ReasonShutdown, N: logs,
	})
	w.settle(entries, fmt.Errorf("writer: abandoned at shutdown deadline: %w", trace.ErrShuttingDown))
}

// drain runs after ctx ends: it refuses new items and jobs, writes queued
// items until the queue is empty or the drain deadline (wctx) passes, abandons
// whatever is left, and then writes the pending counters, including the
// abandoned items' dropped{shutdown} counts, within the counter reserve (fctx).
func (w *Writer) drain(wctx, fctx context.Context) error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	close(w.closing)
	w.failQueuedJobs()
	for w.hasPending() && wctx.Err() == nil {
		w.flush(wctx)
	}
	for w.hasPending() {
		entries, counts := w.take()
		w.restoreCounts(counts)
		w.abandon(entries)
	}
	w.failQueuedJobs()
	if w.hasCounts() {
		w.flush(fctx)
	}
	if w.hasCounts() {
		w.log.Error("ingest counters not stored at shutdown")
	}
	// Only the shutdown deadline abandons items, and Run runs once, so the
	// counter covers exactly this shutdown (including a batch abandoned by the
	// flush that was in progress when ctx ended).
	if lost := w.stats.droppedShutdown.Load(); lost > 0 {
		w.log.Error("shutdown drain incomplete", "count", lost)
		return fmt.Errorf("%w: %d spans and log records not written", ErrDrainIncomplete, lost)
	}
	return nil
}
