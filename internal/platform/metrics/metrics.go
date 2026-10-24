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

const (
	MaxModelLabels    = 50
	MaxEndpointLabels = 64
	OtherLabel        = "other"
)

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

type Sources struct {
	Writer        func() writer.Stats
	Retention     func() retention.Stats
	InflightBytes func() int64
}

type BuildInfo struct {
	Version   string
	Commit    string
	GoVersion string
}

// Metrics owns the registry and the recorded counters.
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
	rejected  map[reasonKey]int64
	dropped   map[reasonKey]int64
	discarded map[reasonKey]int64
	spansRej  int64
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

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

func (m *Metrics) Gatherer() prometheus.Gatherer { return m.reg }

func (m *Metrics) ObserveBatch(d time.Duration, spans int) {
	m.batchDuration.Observe(d.Seconds())
	m.batchSpans.Observe(float64(spans))
}

func (m *Metrics) ObserveQuery(endpoint string, d time.Duration) {
	m.queryDuration.WithLabelValues(m.cap(m.endpoints, endpoint, MaxEndpointLabels)).Observe(d.Seconds())
}

func (m *Metrics) AddUnpriced(model string, n int64) {
	if n <= 0 {
		return
	}
	m.unpriced.WithLabelValues(m.cap(m.models, model, MaxModelLabels)).Add(float64(n))
}

func (m *Metrics) AddUsageDuplicate(kind string, n int64) {
	if n <= 0 {
		return
	}
	m.usageDup.WithLabelValues(oneOf(kind, kinds)).Add(float64(n))
}

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
