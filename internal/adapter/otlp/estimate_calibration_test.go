// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/bench/gen"
	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const (
	// calibrationSpans is the calibration sample size: 100k spans of the
	// benchmark payload mix (bench/gen, capture off).
	calibrationSpans = 100_000
	// calibrationBodies is the number of distinct benchmark requests generated
	// (16,384 spans); they are sent in turn until enough spans are queued.
	// Every request has the exact kind mix and nothing on the path
	// deduplicates across requests, so repeating them changes the heap held
	// per span only through the sample of payload sizes, and it keeps the
	// generator's cost out of a test that runs under the race detector.
	calibrationBodies = 32
)

// retainQueue is an ingest.Queue that keeps every batch it is given, as the
// writer queue holds them until commit, and sums the estimated bytes.
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

// heapAlloc returns the live heap after a full collection. Two cycles also
// empty the sync.Pool victim caches the receiver may have filled.
func heapAlloc() uint64 {
	var ms runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestEstimate_CalibratedOnBenchmarkMix is the byte-estimator calibration test:
// 100k spans of the benchmark payload mix go through the production receive path (OTLP/HTTP
// protobuf decode, conversion, Ingestor translation with the default content
// policy and pricing) into a queue that retains the batches; the sum of the
// estimates the Ingestor passes to Enqueue must be within ±25% of the measured
// heap delta. The ingest benchmark runs with capture off; capture on is checked as well, on
// one pass over the generated requests to bound the time under -race.
//
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

// measureQueuedHeap sends the request bodies in turn through an OTLP/HTTP
// receiver into a retaining queue until it holds at least minSpans spans. It
// returns the spans queued, the estimated bytes and the heap delta.
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
