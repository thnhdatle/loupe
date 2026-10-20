// SPDX-License-Identifier: Apache-2.0

package retention_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/testutil"
	"github.com/thnhdatle/loupe/internal/testutil/fakespanstore"
)

// start runs p.Run in a goroutine. done is closed when Run returns; stop
// cancels Run's context, waits for it and returns its error. Cleanup stops it
// if the test did not.
func start(t *testing.T, p *retention.Planner) (done <-chan struct{}, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	var err error
	go func() {
		defer close(finished)
		err = p.Run(ctx)
	}()
	stop = func() error {
		cancel()
		<-finished
		return err
	}
	t.Cleanup(func() { _ = stop() })
	return finished, stop
}

func (e *env) passes() int64 { return e.p.Stats().Passes }

// advanceUntil advances the fake clock by step until cond holds.
func (e *env) advanceUntil(t *testing.T, step time.Duration, cond func() bool) {
	t.Helper()
	testutil.Eventually(t, func() bool {
		if cond() {
			return true
		}
		e.clk.Advance(step)
		return cond()
	}, testutil.DefaultTimeout)
}

func TestPlanner_Run_PassAtStartThenEveryInterval(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	e := newEnv(t, cfg)
	e.store.addSpan("ages-out", "", ago(cfg.Spans-5*time.Minute), pageSize)
	_, stop := start(t, e.p)

	testutil.Eventually(t, func() bool { return e.passes() >= 1 }, testutil.DefaultTimeout)
	assert.Equal(t, []string{"ages-out"}, e.store.spanIDs(), "younger than retention.spans at the first pass")

	e.advanceUntil(t, cfg.Interval, func() bool { return e.passes() >= 2 })
	testutil.Eventually(t, func() bool { return len(e.store.spanIDs()) == 0 }, testutil.DefaultTimeout)
	require.NoError(t, stop())
}

func TestPlanner_Run_LogsPassErrorsAndContinues(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	e.store.set(func(s *fakeStore) { s.purgeErr[retention.ClassSpans] = errBoom })
	_, stop := start(t, e.p)

	testutil.Eventually(t, func() bool { return e.passes() >= 1 }, testutil.DefaultTimeout)
	assert.Contains(t, e.logs.String(), `level=ERROR msg="retention pass failed"`)
	e.store.set(func(s *fakeStore) { delete(s.purgeErr, retention.ClassSpans) })

	e.advanceUntil(t, time.Minute, func() bool { return e.passes() >= 2 })
	require.NoError(t, stop())
	st := e.p.Stats()
	assert.Equal(t, int64(1), st.PassErrors)
}

func TestPlanner_Run_ReturnsNilWhenWriterShutsDown(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	e.runner.stop()
	done, stop := start(t, e.p)
	select {
	case <-done:
	case <-time.After(testutil.DefaultTimeout):
		t.Fatal("Run did not return")
	}
	require.NoError(t, stop())
	assert.Zero(t, e.p.Stats().PassErrors)
}

func TestPlanner_Run_ReturnsNilOnCancel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		block bool // cancel while a job runs, rather than between passes
	}{
		{name: "between passes"},
		{name: "during a job", block: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, testConfig())
			running := make(chan struct{}, 1)
			if tc.block {
				e.runner.block = func(ctx context.Context, _ string) error {
					running <- struct{}{}
					<-ctx.Done()
					return ctx.Err()
				}
			}
			_, stop := start(t, e.p)
			if tc.block {
				<-running
			} else {
				testutil.Eventually(t, func() bool { return e.passes() >= 1 }, testutil.DefaultTimeout)
			}
			require.NoError(t, stop())
			assert.Zero(t, e.p.Stats().PassErrors)
		})
	}
}

func TestPlanner_Run_WarnsWhenFreeDiskBelowTwiceTheCap(t *testing.T) {
	t.Parallel()
	const capBytes = 64 * mib
	tests := []struct {
		name     string
		cap      int64
		free     int64
		err      error
		wantLog  string
		wantCall bool
	}{
		{name: "below", cap: capBytes, free: 2*capBytes - 1, wantLog: "free disk is below twice", wantCall: true},
		{name: "enough", cap: capBytes, free: 2 * capBytes, wantCall: true},
		{name: "probe fails", cap: capBytes, err: errBoom, wantLog: "free disk check failed", wantCall: true},
		{name: "cap off", cap: 0, free: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			cfg.MaxDBBytes = tc.cap
			called := make(chan struct{}, 1)
			e := newEnv(t, cfg, retention.WithFreeDisk(func(context.Context) (int64, error) {
				called <- struct{}{}
				return tc.free, tc.err
			}))
			_, stop := start(t, e.p)
			testutil.Eventually(t, func() bool { return e.passes() >= 1 }, testutil.DefaultTimeout)
			require.NoError(t, stop())
			assert.Equal(t, tc.wantCall, len(called) == 1)
			logs := e.logs.String()
			for _, msg := range []string{"free disk is below twice", "free disk check failed"} {
				if msg == tc.wantLog {
					assert.Contains(t, logs, "level=WARN msg=\""+msg)
				} else {
					assert.NotContains(t, logs, msg)
				}
			}
		})
	}
}

// writerBatch returns a batch of one span with a unique id.
func writerBatch(i int) *trace.Batch {
	var sid trace.SpanID
	binary.BigEndian.PutUint64(sid[:], uint64(i)+1)
	return &trace.Batch{
		Resources: []trace.Resource{{ID: 1, Attributes: []byte(`{}`)}},
		Spans:     []trace.Span{{TraceID: trace.ID{9}, SpanID: sid, ResourceID: 1, Kind: trace.KindLLM}},
	}
}

// TestPlanner_WithWriter runs the planner against the real single writer:
// ingest batches commit between retention jobs, and the planner stops cleanly
// once the writer shuts down.
func TestPlanner_WithWriter(t *testing.T) {
	t.Parallel()
	spans := fakespanstore.New()
	e := newEnv(t, testConfig())
	w, err := writer.New(spans, e.clk, writer.WithBatchSpans(1), writer.WithShutdownTimeout(time.Second))
	require.NoError(t, err)
	p, err := retention.New(e.store, w, e.clk, testConfig())
	require.NoError(t, err)

	wctx, wcancel := context.WithCancel(context.Background())
	werr := make(chan error, 1)
	go func() { werr <- w.Run(wctx) }()
	stopWriter := sync.OnceValue(func() error {
		wcancel()
		return <-werr
	})
	t.Cleanup(func() { _ = stopWriter() })

	for i := range 7 {
		e.store.addSpan(fmt.Sprintf("old%d", i), "", ago(30*day), pageSize)
	}
	// Each spans job enqueues one ingest batch; the next spans job must find
	// it committed, because the writer runs a ready batch before the next job.
	spansJobs := 0
	var committedBefore []int
	e.store.set(func(s *fakeStore) {
		s.onPurge = func(c retention.Class) {
			if c != retention.ClassSpans {
				return
			}
			committedBefore = append(committedBefore, spans.SpanCount())
			_, err := w.Enqueue(writerBatch(spansJobs), 1, 100, false)
			assert.NoError(t, err)
			spansJobs++
		}
	})
	require.NoError(t, p.Pass(context.Background()))
	assert.Empty(t, e.store.spanIDs())
	assert.Equal(t, []int{0, 1, 2}, committedBefore)
	assert.Empty(t, e.runner.jobNames(), "the fake runner is unused")
	assert.GreaterOrEqual(t, w.Stats().Jobs, int64(len(retention.Classes())+2))

	require.NoError(t, stopWriter())
	require.ErrorIs(t, p.Pass(context.Background()), trace.ErrShuttingDown)
	require.NoError(t, p.Run(context.Background()), "Run returns once the writer has shut down")
}
