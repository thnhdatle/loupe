// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Caps on labels that come from user data or route tables.
const (
	// MaxModelLabels is the number of distinct model label values of
	// loupe_unpriced_spans_total; later models are counted as "other".
	MaxModelLabels = 50
	// MaxEndpointLabels bounds loupe_query_duration_seconds{endpoint}.
	MaxEndpointLabels = 64
	// OtherLabel replaces label values beyond a cap or outside a fixed set.
	OtherLabel = "other"
)

// Fixed label sets. Anything else is reported as OtherLabel.
var (
	rejectReasons = []string{
		trace.ReasonTooManySpans, trace.ReasonRequestTooBig, trace.ReasonInflight,
		trace.ReasonQueueFull, trace.ReasonShutdown, trace.ReasonInvalid,
	}
	dropReasons    = []string{trace.ReasonQueueFull, trace.ReasonStoreError, trace.ReasonShutdown}
	discardReasons = []string{
		trace.ReasonNotStored, trace.ReasonNoSpanContext, trace.ReasonNotGenAI,
	}
	signals = []trace.Signal{trace.SignalTraces, trace.SignalLogs, trace.SignalMetrics}
	kinds   = []string{"llm", "embedding"}
)

// Sources are the snapshots read at scrape time. A nil function is skipped.
type Sources struct {
	Writer    func() writer.Stats
	Retention func() retention.Stats
	// InflightBytes reports the receivers' in-flight decompressed bytes.
	InflightBytes func() int64
}

// BuildInfo labels loupe_build_info.
type BuildInfo struct {
	Version   string
	Commit    string
	GoVersion string
}

// Metrics owns the registry and the recorded counters. It is safe for concurrent use.
type Metrics struct {
	reg *prometheus.Registry
	src Sources

	batchDuration prometheus.Histogram
	batchSpans    prometheus.Histogram
	queryDuration *prometheus.HistogramVec
	usageDup      *prometheus.CounterVec
	unpriced      *prometheus.CounterVec
	stripped      prometheus.Counter

	mu        sync.Mutex
	rejected  map[reasonKey]int64 // traces and logs, by reason
	dropped   map[reasonKey]int64
	discarded map[reasonKey]int64
	spansRej  int64 // traces only
	spansDrop int64
	models    map[string]struct{}
	endpoints map[string]struct{}
}

type reasonKey struct {
	signal trace.Signal
	reason string
}

// New builds the metrics and registers them on a private registry.
func New(src Sources, build BuildInfo) (*Metrics, error) {
	m := &Metrics{
		reg:       prometheus.NewRegistry(),
		src:       src,
		rejected:  map[reasonKey]int64{},
		dropped:   map[reasonKey]int64{},
		discarded: map[reasonKey]int64{},
		models:    map[string]struct{}{},
		endpoints: map[string]struct{}{},
	}
	m.batchDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "loupe_writer_batch_duration_seconds", Help: "Duration of one writer batch commit.",
		Buckets: batchDurationBuckets,
	})
	m.batchSpans = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "loupe_writer_batch_spans", Help: "Spans in one committed writer batch.", Buckets: batchSpanBuckets,
	})
	m.queryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "loupe_query_duration_seconds", Help: "Duration of API and dashboard queries.",
		Buckets: queryDurationBuckets,
	}, []string{"endpoint"})
	m.usageDup = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "loupe_usage_duplicate_spans_total",
		Help: "Spans flagged usage_duplicate, by kind (a later re-evaluation may clear a flag).",
	}, []string{"kind"})
	m.unpriced = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "loupe_unpriced_spans_total",
		Help: "Spans with tokens and no price, by model (top models only, rest as other).",
	}, []string{"model"})
	m.stripped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "loupe_content_stripped_total", Help: "Spans and log records whose content was stripped.",
	})
	for _, k := range kinds {
		m.usageDup.WithLabelValues(k)
	}
	for _, c := range []prometheus.Collector{
		m.batchDuration, m.batchSpans, m.queryDuration, m.usageDup, m.unpriced, m.stripped,
		&collector{m: m}, buildInfoCollector(build),
	} {
		if err := m.reg.Register(c); err != nil {
			return nil, fmt.Errorf("metrics: register: %w", err)
		}
	}
	return m, nil
}

// Handler serves the Prometheus text exposition for /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Gatherer returns the registry, for tests and for merging with other registries.
func (m *Metrics) Gatherer() prometheus.Gatherer { return m.reg }

// ObserveBatch records one committed writer batch.
func (m *Metrics) ObserveBatch(d time.Duration, spans int) {
	m.batchDuration.Observe(d.Seconds())
	m.batchSpans.Observe(float64(spans))
}

// ObserveQuery records one query of an endpoint (a route pattern, never a raw URL).
func (m *Metrics) ObserveQuery(endpoint string, d time.Duration) {
	m.queryDuration.WithLabelValues(m.cap(m.endpoints, endpoint, MaxEndpointLabels)).Observe(d.Seconds())
}

// AddUnpriced counts n spans of model that carry tokens but no price.
func (m *Metrics) AddUnpriced(model string, n int64) {
	if n <= 0 {
		return
	}
	m.unpriced.WithLabelValues(m.cap(m.models, model, MaxModelLabels)).Add(float64(n))
}

// AddUsageDuplicate counts n spans flagged usage_duplicate; kind is "llm" or "embedding".
func (m *Metrics) AddUsageDuplicate(kind string, n int64) {
	if n <= 0 {
		return
	}
	m.usageDup.WithLabelValues(oneOf(kind, kinds)).Add(float64(n))
}

// Count folds one receiver- or ingest-side counter into the ingest metrics. It
// handles rejected, dropped, discarded and content_stripped results; stored and
// duplicate come from writer.Stats, and unpriced from AddUnpriced, so received,
// stored, duplicate and unpriced results are ignored here.
func (m *Metrics) Count(c trace.IngestCount) {
	if c.N <= 0 {
		return
	}
	switch c.Result {
	case trace.ResultContentStripped:
		m.stripped.Add(float64(c.N))
	case trace.ResultRejected:
		m.add(m.rejected, &m.spansRej, c, rejectReasons)
	case trace.ResultDropped:
		m.add(m.dropped, &m.spansDrop, c, dropReasons)
	case trace.ResultDiscarded:
		m.add(m.discarded, nil, c, discardReasons)
	case trace.ResultReceived, trace.ResultStored, trace.ResultDuplicate, trace.ResultUnpriced:
	}
}

func (m *Metrics) add(dst map[reasonKey]int64, spans *int64, c trace.IngestCount, reasons []string) {
	k := reasonKey{signal: c.Signal, reason: oneOf(c.Reason, reasons)}
	if !knownSignal(k.signal) {
		k.signal = OtherLabel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dst[k] += c.N
	if spans != nil && c.Signal == trace.SignalTraces {
		*spans += c.N
	}
}

// cap returns v while fewer than limit distinct values were seen, else OtherLabel.
func (m *Metrics) cap(seen map[string]struct{}, v string, limit int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := seen[v]; ok {
		return v
	}
	if len(seen) >= limit {
		return OtherLabel
	}
	seen[v] = struct{}{}
	return v
}

func oneOf(v string, allowed []string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return OtherLabel
}

func knownSignal(s trace.Signal) bool {
	for _, k := range signals {
		if s == k {
			return true
		}
	}
	return false
}
