// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"maps"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const labelReason = "reason"

var (
	descSpans = prometheus.NewDesc("loupe_ingest_spans_total",
		"Spans by outcome: stored, rejected, dropped, duplicate.", []string{"result"}, nil)
	descRejected = prometheus.NewDesc("loupe_ingest_rejected_total",
		"Spans and log records rejected, by reason.", []string{labelReason}, nil)
	descDropped = prometheus.NewDesc("loupe_ingest_dropped_total",
		"Spans and log records dropped, by reason.", []string{labelReason}, nil)
	descDiscarded = prometheus.NewDesc("loupe_ingest_discarded_total",
		"Items accepted but not stored, by signal and reason.", []string{"signal", labelReason}, nil)
	descQueueSpans = prometheus.NewDesc("loupe_ingest_queue_spans",
		"Slots held by the writer queue.", nil, nil)
	descQueueBytes = prometheus.NewDesc("loupe_ingest_queue_bytes",
		"Estimated bytes held by the writer queue.", nil, nil)
	descInflight = prometheus.NewDesc("loupe_ingest_inflight_bytes",
		"Decompressed request bytes currently in flight in the receivers.", nil, nil)
	descWriterBatches = prometheus.NewDesc("loupe_writer_batches_total", "Committed writer batches.", nil, nil)
	descWriterRetries = prometheus.NewDesc("loupe_writer_retries_total", "Write attempts after the first.", nil, nil)
	descWriterJobs    = prometheus.NewDesc("loupe_writer_jobs_total", "Writer jobs run.", nil, nil)
	descWriterJobErrs = prometheus.NewDesc("loupe_writer_job_errors_total", "Writer jobs that failed.", nil, nil)
	descWriterJobsQ   = prometheus.NewDesc("loupe_writer_queued_jobs", "Jobs waiting for the writer.", nil, nil)
	descDBSize        = prometheus.NewDesc("loupe_db_size_bytes", "Database size: main file, WAL and SHM.", nil, nil)
	descRetDeleted    = prometheus.NewDesc("loupe_retention_deleted_total",
		"Rows deleted by the retention age rules, by class.", []string{"class"}, nil)
	descRetCap = prometheus.NewDesc("loupe_retention_cap_deleted_total",
		"Rows deleted by the disk cap, by class.", []string{"class"}, nil)
	descRetPasses = prometheus.NewDesc("loupe_retention_passes_total", "Retention passes completed.", nil, nil)
	descRetErrors = prometheus.NewDesc("loupe_retention_pass_errors_total", "Retention passes that failed.", nil, nil)
	descRetCapHit = prometheus.NewDesc("loupe_retention_cap_exceeded_total",
		"Passes that found the database above retention.max_db_bytes.", nil, nil)
	descRetCapUnmet = prometheus.NewDesc("loupe_retention_cap_unmet_total",
		"Passes that ended above the cap with only the last hour of data left.", nil, nil)
	descRetCkpt = prometheus.NewDesc("loupe_retention_checkpoint_busy_total",
		"wal_checkpoint(TRUNCATE) calls that reported busy.", nil, nil)
	descBuildInfo = prometheus.NewDesc("loupe_build_info", "Build information.",
		[]string{"version", "commit", "go_version"}, nil)
)

type collector struct{ m *Metrics }

func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		descSpans, descRejected, descDropped, descDiscarded, descQueueSpans, descQueueBytes, descInflight,
		descWriterBatches, descWriterRetries, descWriterJobs, descWriterJobErrs, descWriterJobsQ, descDBSize,
		descRetDeleted, descRetCap, descRetPasses, descRetErrors, descRetCapHit, descRetCapUnmet, descRetCkpt,
	} {
		ch <- d
	}
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {
	m := c.m
	counter := func(d *prometheus.Desc, v int64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(v), labels...)
	}
	gauge := func(d *prometheus.Desc, v int64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, float64(v))
	}

	var ws writerView
	if m.src.Writer != nil {
		s := m.src.Writer()
		ws = writerView{
			inserted: s.Inserted, duplicates: s.Duplicates, storeErr: s.DroppedStoreError, shutdown: s.DroppedShutdown,
		}
		gauge(descQueueSpans, int64(s.QueuedSlots))
		gauge(descQueueBytes, s.QueuedBytes)
		gauge(descWriterJobsQ, int64(s.QueuedJobs))
		counter(descWriterBatches, s.Batches)
		counter(descWriterRetries, s.Retries)
		counter(descWriterJobs, s.Jobs)
		counter(descWriterJobErrs, s.JobErrors)
	}
	if m.src.InflightBytes != nil {
		gauge(descInflight, m.src.InflightBytes())
	}

	m.mu.Lock()
	rejected, dropped, discarded := maps.Clone(m.rejected), maps.Clone(m.dropped), maps.Clone(m.discarded)
	spansRej, spansDrop := m.spansRej, m.spansDrop
	m.mu.Unlock()

	counter(descSpans, ws.inserted, "stored")
	counter(descSpans, spansRej, "rejected")
	counter(descSpans, spansDrop+ws.storeErr+ws.shutdown, "dropped")
	counter(descSpans, ws.duplicates, "duplicate")
	emitReasons(counter, descRejected, rejected, rejectReasons, nil)
	emitReasons(counter, descDropped, dropped, dropReasons, map[string]int64{
		trace.ReasonStoreError: ws.storeErr, trace.ReasonShutdown: ws.shutdown,
	})
	for _, k := range sortedKeys(discarded) {
		counter(descDiscarded, discarded[k], string(k.signal), k.reason)
	}

	if m.src.Retention != nil {
		emitRetention(counter, gauge, m.src.Retention())
	}
}

type writerView struct{ inserted, duplicates, storeErr, shutdown int64 }

func emitReasons(counter func(*prometheus.Desc, int64, ...string), d *prometheus.Desc,
	seen map[reasonKey]int64, reasons []string, extra map[string]int64,
) {
	sums := map[string]int64{}
	for k, n := range seen {
		sums[k.reason] += n
	}
	for r, n := range extra {
		sums[r] += n
	}
	for _, r := range reasons {
		counter(d, sums[r], r)
	}
	if n := sums[OtherLabel]; n > 0 {
		counter(d, n, OtherLabel)
	}
}

func sortedKeys(m map[reasonKey]int64) []reasonKey {
	keys := slices.Collect(maps.Keys(m))
	slices.SortFunc(keys, func(a, b reasonKey) int {
		return strings.Compare(string(a.signal)+"/"+a.reason, string(b.signal)+"/"+b.reason)
	})
	return keys
}

func emitRetention(counter func(*prometheus.Desc, int64, ...string), gauge func(*prometheus.Desc, int64),
	s retention.Stats,
) {
	for _, cl := range retention.Classes() {
		counter(descRetDeleted, s.Deleted[cl], string(cl))
	}
	for _, cl := range []retention.Class{retention.ClassContent, retention.ClassSpans} {
		counter(descRetCap, s.CapDeleted[cl], string(cl))
	}
	counter(descRetPasses, s.Passes)
	counter(descRetErrors, s.PassErrors)
	counter(descRetCapHit, s.CapExceeded)
	counter(descRetCapUnmet, s.CapUnmet)
	counter(descRetCkpt, s.CheckpointBusy)
	gauge(descDBSize, s.DBSizeBytes)
}

type buildInfoCollector BuildInfo

func (buildInfoCollector) Describe(ch chan<- *prometheus.Desc) { ch <- descBuildInfo }

func (b buildInfoCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(descBuildInfo, prometheus.GaugeValue, 1, b.Version, b.Commit, b.GoVersion)
}

func WrapQueue(q ingest.Queue, m *Metrics) ingest.Queue { return &countingQueue{Queue: q, m: m} }

type countingQueue struct {
	ingest.Queue
	m *Metrics
}

func (q *countingQueue) AddCount(c trace.IngestCount) {
	q.m.Count(c)
	q.Queue.AddCount(c)
}
