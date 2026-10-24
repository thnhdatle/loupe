// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/testutil/gen"
)

const (
	calibrationSpans  = 100_000
	calibrationBodies = 32
)

type retainQueue struct {
	batches []*trace.Batch
	spans   int
	bytes   int64
}

func (q *retainQueue) Enqueue(b *trace.Batch, slots int, bytes int64, _ bool) (<-chan error, error) {
	q.batches = append(q.batches, b)
	q.spans += slots
	q.bytes += bytes
	return nil, nil //nolint:nilnil // ingest.Queue contract: no channel without wait.
}

func (*retainQueue) AddCount(trace.IngestCount) {}

func (*retainQueue) HasRoom(int) bool { return true }

func heapAlloc() uint64 {
	var ms runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

//nolint:paralleltest // measures the process heap; parallel tests would distort it.
func TestEstimate_CalibratedOnBenchmarkMix(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	p := gen.NewPayload(1, 2)
	bodies := make([]httpReq, calibrationBodies)
	for i := range bodies {
		bodies[i] = protoReq(t, "/v1/traces", p.Request(uint32(i+1), now))
	}
	tests := []struct {
		mode     content.Mode
		minSpans int
	}{
		{mode: content.ModeOff, minSpans: calibrationSpans},
		{mode: content.ModeOn, minSpans: calibrationBodies * gen.PayloadSpans},
	}
	for _, tc := range tests {
		mode := tc.mode
		t.Run(string(mode), func(t *testing.T) {
			spans, estimate, delta := measureQueuedHeap(t, mode, now, bodies, tc.minSpans)
			require.GreaterOrEqual(t, spans, tc.minSpans)
			ratio := float64(estimate) / float64(delta)
			t.Logf("capture %s: %d spans, heap delta %d B (%.0f B/span), estimate %d B (%.0f B/span), ratio %.3f",
				mode, spans, delta, float64(delta)/float64(spans), estimate, float64(estimate)/float64(spans), ratio)
			require.InDelta(t, 1.0, ratio, 0.25, "estimate/heap delta")
		})
	}
}

func measureQueuedHeap(t *testing.T, mode content.Mode, now time.Time, bodies []httpReq, minSpans int) (
	spans int, estimate, delta int64,
) {
	t.Helper()
	cfg := config.Default()
	table, err := pricingfile.Load("")
	require.NoError(t, err)
	calc, err := cost.NewCalculator(table)
	require.NoError(t, err)
	red, err := content.NewRedactor(cfg.Content.RedactPresets(), cfg.Content.RedactRules())
	require.NoError(t, err)
	q := &retainQueue{}
	ing, err := ingest.New(q, calc, clock.NewFake(now),
		ingest.WithContentPolicy(content.Policy{Mode: mode, MaxBytes: int(cfg.Content.MaxBytes), Redactor: red}),
		ingest.WithMaxAttributeBytes(int(cfg.Receiver.MaxAttributeBytes)))
	require.NoError(t, err)
	lim, err := ingest.NewInflightLimiter(ingest.DefaultMaxInflightBytes)
	require.NoError(t, err)
	srv, err := otlp.NewServer(otlp.DefaultConfig(), ing, lim)
	require.NoError(t, err)
	h := srv.HTTPHandler()

	before := heapAlloc()
	for i := 0; q.spans < minSpans; i++ {
		rec := do(t, h, bodies[i%len(bodies)])
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	after := heapAlloc()
	runtime.KeepAlive(q)
	return q.spans, q.bytes, int64(after) - int64(before)
}
