// SPDX-License-Identifier: Apache-2.0

package writer_test

import (
	"context"
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/testutil"
	"github.com/thnhdatle/loupe/internal/testutil/fakespanstore"
)

var (
	epoch  = time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	nextID atomic.Uint64
)

const (
	interval = 200 * time.Millisecond
	wait     = testutil.DefaultTimeout
)

// spans returns a batch of n spans with ids unique within the test binary.
func spans(n int) *trace.Batch {
	b := &trace.Batch{Resources: []trace.Resource{{ID: 7, Attributes: []byte(`{}`)}}}
	for range n {
		var sid trace.SpanID
		binary.BigEndian.PutUint64(sid[:], nextID.Add(1))
		b.Spans = append(b.Spans, trace.Span{TraceID: trace.ID{1}, SpanID: sid, ResourceID: 7, Kind: trace.KindLLM})
	}
	return b
}

type fixture struct {
	t     *testing.T
	store *fakespanstore.Store
	clk   *clock.Fake
	w     *writer.Writer
}

func newFixture(t *testing.T, opts ...writer.Option) *fixture {
	t.Helper()
	store := fakespanstore.New()
	clk := clock.NewFake(epoch)
	base := []writer.Option{
		writer.WithBatchSpans(4),
		writer.WithBatchInterval(interval),
		writer.WithQueueLimits(100, 1<<20),
		writer.WithRetry(3, 10*time.Millisecond, 40*time.Millisecond),
		writer.WithShutdownTimeout(time.Second),
	}
	w, err := writer.New(store, clk, append(base, opts...)...)
	require.NoError(t, err)
	return &fixture{t: t, store: store, clk: clk, w: w}
}

// run starts the writer; the returned stop cancels it and returns Run's error.
// Cleanup stops it if the test did not.
func (f *fixture) run() (stop func() error) {
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- f.w.Run(ctx) }()
	var (
		stopped bool
		err     error
	)
	stop = func() error {
		if !stopped {
			stopped = true
			cancel()
			err = <-errc
		}
		return err
	}
	f.t.Cleanup(func() { _ = stop() })
	return stop
}

func (f *fixture) enqueue(n int, ack bool) <-chan error {
	f.t.Helper()
	ch, err := f.w.Enqueue(spans(n), n, int64(n)*100, ack)
	require.NoError(f.t, err)
	return ch
}

// advanceUntil advances the fake clock by step until cond holds.
func (f *fixture) advanceUntil(step time.Duration, cond func() bool) {
	f.t.Helper()
	testutil.Eventually(f.t, func() bool {
		if cond() {
			return true
		}
		f.clk.Advance(step)
		return cond()
	}, wait)
}

// spanTotal counts spans over committed batches.
func spanTotal(bs []trace.Batch) int {
	n := 0
	for i := range bs {
		n += len(bs[i].Spans)
	}
	return n
}

// receive waits for one value on ch.
func receive(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(wait):
		t.Fatal("no value received")
		return nil
	}
}
