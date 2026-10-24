// SPDX-License-Identifier: Apache-2.0

package ingest_test

import (
	"context"
	"encoding/binary"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/testutil"
	"github.com/thnhdatle/loupe/internal/testutil/fakespanstore"
)

const (
	interval  = 200 * time.Millisecond
	wait      = testutil.DefaultTimeout
	wantNanos = 450_000
)

var (
	epoch  = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	nextID atomic.Uint64
	res    = &trace.RawResource{Attributes: []trace.Attr{sattr("service.name", "checkout")}}
)

func sattr(k, v string) trace.Attr { return trace.Attr{Key: k, Value: trace.StringValue(v)} }

func iattr(k string, v int64) trace.Attr { return trace.Attr{Key: k, Value: trace.IntValue(v)} }

func newIDs() (trace.ID, trace.SpanID) {
	n := nextID.Add(1)
	var tid trace.ID
	var sid trace.SpanID
	binary.BigEndian.PutUint64(tid[8:], n)
	binary.BigEndian.PutUint64(sid[:], n)
	return tid, sid
}

func chatSpan(extra ...trace.Attr) trace.RawSpan {
	tid, sid := newIDs()
	attrs := []trace.Attr{
		sattr("gen_ai.operation.name", "chat"),
		sattr("gen_ai.provider.name", "openai"),
		sattr("gen_ai.request.model", "gpt-4o-mini"),
		iattr("gen_ai.usage.input_tokens", 1000),
		iattr("gen_ai.usage.output_tokens", 500),
	}
	for _, e := range extra {
		i := slices.IndexFunc(attrs, func(a trace.Attr) bool { return a.Key == e.Key })
		if i >= 0 {
			attrs[i] = e
		} else {
			attrs = append(attrs, e)
		}
	}
	return trace.RawSpan{
		Resource: res, TraceID: tid, SpanID: sid, Name: "chat gpt-4o-mini",
		StartNs: epoch.UnixNano(), EndNs: epoch.Add(time.Second).UnixNano(),
		Attributes: attrs,
	}
}

func calculator(t *testing.T) *cost.Calculator {
	t.Helper()
	tbl, err := cost.NewPriceTable([]cost.Price{{
		Match: cost.MatchExact, Provider: "openai", Model: "gpt-4o-mini",
		InputPerMtok: 150_000_000, OutputPerMtok: 600_000_000,
		EffectiveFrom: epoch.AddDate(-1, 0, 0), AsOf: epoch, Source: "test",
	}}, nil)
	require.NoError(t, err)
	calc, err := cost.NewCalculator(tbl)
	require.NoError(t, err)
	return calc
}

type fixture struct {
	t     *testing.T
	store *fakespanstore.Store
	clk   *clock.Fake
	w     *writer.Writer
	in    *ingest.Ingestor
}

func newFixture(t *testing.T, wopts []writer.Option, iopts ...ingest.Option) *fixture {
	t.Helper()
	store := fakespanstore.New()
	clk := clock.NewFake(epoch)
	base := []writer.Option{
		writer.WithBatchSpans(1000),
		writer.WithBatchInterval(interval),
		writer.WithRetry(1, time.Millisecond, time.Millisecond),
		writer.WithShutdownTimeout(time.Second),
	}
	w, err := writer.New(store, clk, append(base, wopts...)...)
	require.NoError(t, err)
	in, err := ingest.New(w, calculator(t), clk, iopts...)
	require.NoError(t, err)
	return &fixture{t: t, store: store, clk: clk, w: w, in: in}
}

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

func (f *fixture) flushUntil(cond func() bool) {
	f.t.Helper()
	testutil.Eventually(f.t, func() bool {
		if cond() {
			return true
		}
		f.clk.Advance(interval)
		return cond()
	}, wait)
}

func (f *fixture) count(sig trace.Signal, r trace.Result, reason string) int64 {
	return f.store.Counts()[fakespanstore.CountKey{Signal: sig, Result: r, Reason: reason}]
}

type result struct {
	res trace.IngestResult
	err error
}

func async(fn func() (trace.IngestResult, error)) <-chan result {
	ch := make(chan result, 1)
	go func() {
		r, err := fn()
		ch <- result{res: r, err: err}
	}()
	return ch
}

func receive(t *testing.T, ch <-chan result) result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(wait):
		t.Fatal("no result")
		return result{}
	}
}
