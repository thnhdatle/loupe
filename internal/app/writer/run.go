// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Run flushes batches and runs jobs until ctx ends, then drains the queue.
func (w *Writer) Run(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer close(w.stopped)
	wctx, fctx, stopDeadline := w.shutdownDeadline(ctx)
	defer stopDeadline()
	tick := w.clk.NewTicker(w.cfg.batchInterval)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return w.drain(wctx, fctx)
		}
		if w.batchReady() {
			w.flush(wctx)
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

func (w *Writer) counterReserve() time.Duration { return min(w.cfg.shutdownTimeout/10, time.Second) }

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
		w.restoreCounts(counts)
		w.abandon(entries)
	default:
		w.dropAfterRetries(entries, counts, err)
	}
}

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

func (w *Writer) backoff(n int) time.Duration {
	d := w.cfg.retryBase
	for i := 1; i < n && d < w.cfg.retryMax; i++ {
		d *= 2
	}
	d = min(d, w.cfg.retryMax)
	half := d / 2
	return half + time.Duration(w.cfg.rnd.Int64N(int64(d-half)+1))
}

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
	if lost := w.stats.droppedShutdown.Load(); lost > 0 {
		w.log.Error("shutdown drain incomplete", "count", lost)
		return fmt.Errorf("%w: %d spans and log records not written", ErrDrainIncomplete, lost)
	}
	return nil
}
