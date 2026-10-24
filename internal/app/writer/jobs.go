// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"context"
	"fmt"
	"sync"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type jobRequest struct {
	job  trace.Job
	done chan error

	mu        sync.Mutex
	abandoned bool
	cancel    context.CancelFunc
}

func (r *jobRequest) abandon() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.abandoned = true
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *jobRequest) begin(cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.abandoned {
		return false
	}
	r.cancel = cancel
	return true
}

func (r *jobRequest) finish(err error) {
	r.mu.Lock()
	r.cancel = nil
	r.mu.Unlock()
	r.done <- err
}

// Submit runs j on the writer goroutine between batches and waits for the result.
func (w *Writer) Submit(ctx context.Context, j trace.Job) error {
	if j.Name == "" || j.Run == nil {
		return fmt.Errorf("%w: name and Run are required", ErrInvalidJob)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-w.closing:
		return trace.ErrShuttingDown
	default:
	}
	req := &jobRequest{job: j, done: make(chan error, 1)}
	select {
	case w.jobs <- req:
	case <-w.closing:
		return trace.ErrShuttingDown
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		req.abandon()
		return ctx.Err()
	case <-w.stopped:
		select {
		case err := <-req.done:
			return err
		default:
			return trace.ErrShuttingDown
		}
	}
}

func (w *Writer) runJob(wctx context.Context, req *jobRequest) {
	jctx, cancel := context.WithCancel(wctx)
	defer cancel()
	if !req.begin(cancel) {
		req.finish(context.Canceled)
		return
	}
	start := w.clk.Now()
	err := runSafely(jctx, req.job)
	w.stats.jobs.Add(1)
	if err != nil {
		w.stats.jobErrors.Add(1)
	}
	w.log.Debug("job finished", "operation", req.job.Name,
		"duration_ms", w.clk.Now().Sub(start).Milliseconds(), "status", statusOf(err))
	req.finish(err)
}

func runSafely(ctx context.Context, j trace.Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("writer: job %s panicked: %v", j.Name, r)
		}
	}()
	if err := j.Run(ctx); err != nil {
		return fmt.Errorf("writer: job %s: %w", j.Name, err)
	}
	return nil
}

func statusOf(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func (w *Writer) failQueuedJobs() {
	for {
		select {
		case req := <-w.jobs:
			req.finish(trace.ErrShuttingDown)
		default:
			return
		}
	}
}
