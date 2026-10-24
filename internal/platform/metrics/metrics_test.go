// SPDX-License-Identifier: Apache-2.0

package metrics_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/metrics"
)

type snapshot map[string]*dto.MetricFamily

func gather(t *testing.T, m *metrics.Metrics) snapshot {
	t.Helper()
	fams, err := m.Gatherer().Gather()
	require.NoError(t, err)
	s := snapshot{}
	for _, f := range fams {
		s[f.GetName()] = f
	}
	return s
}

func (s snapshot) value(name string, labels ...string) (float64, bool) {
	f, ok := s[name]
	if !ok {
		return 0, false
	}
	for _, mt := range f.GetMetric() {
		if !matches(mt, labels) {
			continue
		}
		switch {
		case mt.GetCounter() != nil:
			return mt.GetCounter().GetValue(), true
		case mt.GetGauge() != nil:
			return mt.GetGauge().GetValue(), true
		case mt.GetHistogram() != nil:
			return float64(mt.GetHistogram().GetSampleCount()), true
		}
	}
	return 0, false
}

func (s snapshot) series(name string) int { return len(s[name].GetMetric()) }

func matches(mt *dto.Metric, labels []string) bool {
	if len(mt.GetLabel()) != len(labels)/2 {
		return false
	}
	for i := 0; i < len(labels); i += 2 {
		found := false
		for _, l := range mt.GetLabel() {
			if l.GetName() == labels[i] && l.GetValue() == labels[i+1] {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s snapshot) want(t *testing.T, want float64, name string, labels ...string) {
	t.Helper()
	got, ok := s.value(name, labels...)
	require.Truef(t, ok, "series %s%v missing", name, labels)
	assert.InDeltaf(t, want, got, 0, "%s%v", name, labels)
}

func rejected(sig trace.Signal, reason string, n int64) trace.IngestCount {
	return trace.IngestCount{Signal: sig, Result: trace.ResultRejected, Reason: reason, N: n}
}

func newMetrics(t *testing.T, src metrics.Sources) *metrics.Metrics {
	t.Helper()
	m, err := metrics.New(src, metrics.BuildInfo{Version: "v1", Commit: "abc", GoVersion: "go1.27"})
	require.NoError(t, err)
	return m
}

func TestSnapshotsBecomeMetrics(t *testing.T) {
	t.Parallel()
	m := newMetrics(t, metrics.Sources{
		Writer: func() writer.Stats {
			return writer.Stats{
				QueuedSlots: 12, QueuedBytes: 3456, QueuedJobs: 2, Batches: 7, Inserted: 100, Duplicates: 4,
				Retries: 3, DroppedStoreError: 5, DroppedShutdown: 6, Jobs: 8, JobErrors: 1,
			}
		},
		Retention: func() retention.Stats {
			return retention.Stats{
				Passes: 9, PassErrors: 1, CapExceeded: 2, CapUnmet: 1, CheckpointBusy: 3, DBSizeBytes: 4096,
				Deleted:    map[retention.Class]int64{retention.ClassSpans: 50, retention.ClassContent: 20},
				CapDeleted: map[retention.Class]int64{retention.ClassContent: 7},
			}
		},
		InflightBytes: func() int64 { return 999 },
	})
	m.Count(rejected(trace.SignalTraces, trace.ReasonQueueFull, 10))
	m.Count(rejected(trace.SignalLogs, trace.ReasonTooManySpans, 3))
	m.Count(trace.IngestCount{
		Signal: trace.SignalTraces, Result: trace.ResultDropped, Reason: trace.ReasonQueueFull, N: 2,
	})

	s := gather(t, m)
	s.want(t, 12, "loupe_ingest_queue_spans")
	s.want(t, 3456, "loupe_ingest_queue_bytes")
	s.want(t, 999, "loupe_ingest_inflight_bytes")
	s.want(t, 2, "loupe_writer_queued_jobs")
	s.want(t, 7, "loupe_writer_batches_total")
	s.want(t, 3, "loupe_writer_retries_total")
	s.want(t, 8, "loupe_writer_jobs_total")
	s.want(t, 1, "loupe_writer_job_errors_total")
	s.want(t, 100, "loupe_ingest_spans_total", "result", "stored")
	s.want(t, 4, "loupe_ingest_spans_total", "result", "duplicate")
	s.want(t, 10, "loupe_ingest_spans_total", "result", "rejected")
	s.want(t, 13, "loupe_ingest_spans_total", "result", "dropped")
	s.want(t, 10, "loupe_ingest_rejected_total", "reason", "queue_full")
	s.want(t, 3, "loupe_ingest_rejected_total", "reason", "too_many_spans")
	s.want(t, 0, "loupe_ingest_rejected_total", "reason", "inflight_limit")
	s.want(t, 2, "loupe_ingest_dropped_total", "reason", "queue_full")
	s.want(t, 5, "loupe_ingest_dropped_total", "reason", "store_error")
	s.want(t, 6, "loupe_ingest_dropped_total", "reason", "shutdown")
	s.want(t, 4096, "loupe_db_size_bytes")
	s.want(t, 50, "loupe_retention_deleted_total", "class", "spans")
	s.want(t, 20, "loupe_retention_deleted_total", "class", "content")
	s.want(t, 0, "loupe_retention_deleted_total", "class", "orphan_content")
	s.want(t, 7, "loupe_retention_cap_deleted_total", "class", "content")
	s.want(t, 0, "loupe_retention_cap_deleted_total", "class", "spans")
	s.want(t, 9, "loupe_retention_passes_total")
	s.want(t, 1, "loupe_retention_pass_errors_total")
	s.want(t, 2, "loupe_retention_cap_exceeded_total")
	s.want(t, 1, "loupe_retention_cap_unmet_total")
	s.want(t, 3, "loupe_retention_checkpoint_busy_total")
	s.want(t, 1, "loupe_build_info", "version", "v1", "commit", "abc", "go_version", "go1.27")
	assert.Equal(t, len(retention.Classes()), s.series("loupe_retention_deleted_total"))
}

func TestNilSourcesStillServe(t *testing.T) {
	t.Parallel()
	m := newMetrics(t, metrics.Sources{})
	s := gather(t, m)
	s.want(t, 0, "loupe_ingest_spans_total", "result", "stored")
	_, ok := s.value("loupe_ingest_queue_spans")
	assert.False(t, ok)
	_, ok = s.value("loupe_db_size_bytes")
	assert.False(t, ok)
}

func TestCountRouting(t *testing.T) {
	t.Parallel()
	m := newMetrics(t, metrics.Sources{})
	for _, c := range []trace.IngestCount{
		{Signal: trace.SignalMetrics, Result: trace.ResultDiscarded, Reason: trace.ReasonNotStored, N: 5},
		{Signal: trace.SignalMetrics, Result: trace.ResultDiscarded, Reason: trace.ReasonNotStored, N: 2},
		{Signal: trace.SignalLogs, Result: trace.ResultDiscarded, Reason: trace.ReasonNotGenAI, N: 1},
		{Signal: trace.SignalTraces, Result: trace.ResultContentStripped, N: 4},
		{Signal: trace.SignalLogs, Result: trace.ResultContentStripped, N: 1},
		{Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: "weird-reason-123", N: 3},
		{Signal: trace.Signal("custom"), Result: trace.ResultDiscarded, Reason: "x", N: 1},
		{Signal: trace.SignalTraces, Result: trace.ResultStored, N: 100},
		{Signal: trace.SignalTraces, Result: trace.ResultDuplicate, N: 100},
		{Signal: trace.SignalTraces, Result: trace.ResultReceived, N: 100},
		{Signal: trace.SignalTraces, Result: trace.ResultUnpriced, N: 100},
		{Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonQueueFull, N: 0},
		{Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonQueueFull, N: -4},
	} {
		m.Count(c)
	}
	s := gather(t, m)
	s.want(t, 7, "loupe_ingest_discarded_total", "signal", "metrics", "reason", "not_stored")
	s.want(t, 1, "loupe_ingest_discarded_total", "signal", "logs", "reason", "not_gen_ai")
	s.want(t, 1, "loupe_ingest_discarded_total", "signal", "other", "reason", "other")
	s.want(t, 5, "loupe_content_stripped_total")
	s.want(t, 3, "loupe_ingest_rejected_total", "reason", "other")
	s.want(t, 0, "loupe_ingest_rejected_total", "reason", "queue_full")
	s.want(t, 0, "loupe_ingest_spans_total", "result", "stored")
	s.want(t, 3, "loupe_ingest_spans_total", "result", "rejected")
	_, ok := s.value("loupe_unpriced_spans_total", "model", "other")
	assert.False(t, ok, "unpriced results are not taken from Count")
}

func TestLabelCardinalityIsBounded(t *testing.T) {
	t.Parallel()
	m := newMetrics(t, metrics.Sources{})
	for i := range 500 {
		m.AddUnpriced(fmt.Sprintf("model-%d", i), 1)
		m.ObserveQuery(fmt.Sprintf("/v1/endpoint-%d", i), time.Millisecond)
		m.Count(trace.IngestCount{Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: fmt.Sprint("r", i), N: 1})
		m.Count(trace.IngestCount{
			Signal: trace.Signal(fmt.Sprint("s", i)), Result: trace.ResultDiscarded, Reason: fmt.Sprint("r", i), N: 1,
		})
		m.AddUsageDuplicate(fmt.Sprint("kind", i), 1)
	}
	m.AddUnpriced("model-0", 4)
	s := gather(t, m)
	assert.Equal(t, metrics.MaxModelLabels+1, s.series("loupe_unpriced_spans_total"))
	s.want(t, 5, "loupe_unpriced_spans_total", "model", "model-0")
	s.want(t, float64(500-metrics.MaxModelLabels), "loupe_unpriced_spans_total", "model", "other")
	assert.Equal(t, metrics.MaxEndpointLabels+1, s.series("loupe_query_duration_seconds"))
	assert.Equal(t, 1, s.series("loupe_ingest_discarded_total"), "unknown signals and reasons collapse")
	assert.Equal(t, 3, s.series("loupe_usage_duplicate_spans_total"))
	s.want(t, 500, "loupe_usage_duplicate_spans_total", "kind", "other")
}

func TestObservations(t *testing.T) {
	t.Parallel()
	m := newMetrics(t, metrics.Sources{})
	m.ObserveBatch(30*time.Millisecond, 2000)
	m.ObserveBatch(5*time.Millisecond, 10)
	m.ObserveQuery("/v1/traces", 120*time.Millisecond)
	m.AddUnpriced("gpt-x", 3)
	m.AddUnpriced("gpt-x", 0)
	m.AddUsageDuplicate("llm", 2)
	m.AddUsageDuplicate("embedding", 1)
	m.AddUsageDuplicate("llm", -1)

	s := gather(t, m)
	h := s["loupe_writer_batch_duration_seconds"].GetMetric()[0].GetHistogram()
	assert.EqualValues(t, 2, h.GetSampleCount())
	assert.InDelta(t, 0.035, h.GetSampleSum(), 1e-9)
	s.want(t, 2, "loupe_writer_batch_spans")
	s.want(t, 1, "loupe_query_duration_seconds", "endpoint", "/v1/traces")
	s.want(t, 3, "loupe_unpriced_spans_total", "model", "gpt-x")
	s.want(t, 2, "loupe_usage_duplicate_spans_total", "kind", "llm")
	s.want(t, 1, "loupe_usage_duplicate_spans_total", "kind", "embedding")
}

func TestHandlerServesTextExposition(t *testing.T) {
	t.Parallel()
	m := newMetrics(t, metrics.Sources{Writer: func() writer.Stats { return writer.Stats{Inserted: 3} }})
	m.ObserveBatch(time.Millisecond, 1)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "text/plain")
	text := string(body)
	for _, want := range []string{
		"# TYPE loupe_ingest_spans_total counter",
		`loupe_ingest_spans_total{result="stored"} 3`,
		"# TYPE loupe_writer_batch_duration_seconds histogram",
		`loupe_build_info{commit="abc",go_version="go1.27",version="v1"} 1`,
	} {
		assert.Contains(t, text, want)
	}
	assert.NotContains(t, text, "\x00")
}

type fakeQueue struct{ counts []trace.IngestCount }

func (*fakeQueue) Enqueue(*trace.Batch, int, int64, bool) (<-chan error, error) {
	return make(chan error), nil
}

func (q *fakeQueue) AddCount(c trace.IngestCount) { q.counts = append(q.counts, c) }

func (*fakeQueue) HasRoom(int) bool { return true }

func TestWrapQueueCountsAndDelegates(t *testing.T) {
	t.Parallel()
	m := newMetrics(t, metrics.Sources{})
	inner := &fakeQueue{}
	q := metrics.WrapQueue(inner, m)
	c := trace.IngestCount{Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonInflight, N: 6}
	q.AddCount(c)
	assert.Equal(t, []trace.IngestCount{c}, inner.counts)
	gather(t, m).want(t, 6, "loupe_ingest_rejected_total", "reason", "inflight_limit")
}

func TestConcurrentRecordingAndScraping(t *testing.T) {
	t.Parallel()
	m := newMetrics(t, metrics.Sources{Writer: func() writer.Stats { return writer.Stats{} }})
	done := make(chan struct{})
	for i := range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 200 {
				m.Count(rejected(trace.SignalTraces, trace.ReasonQueueFull, 1))
				m.AddUnpriced(fmt.Sprint("m", i), 1)
				m.ObserveQuery("/v1/x", time.Millisecond)
				_, _ = m.Gatherer().Gather()
			}
		}()
	}
	for range 4 {
		<-done
	}
	gather(t, m).want(t, 800, "loupe_ingest_spans_total", "result", "rejected")
}
