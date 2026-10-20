// SPDX-License-Identifier: Apache-2.0

package trace

import "context"

// Ingestor accepts decoded OTLP data from the receivers. Implemented by app/ingest.
//
// IngestSpans and IngestLogs return ErrOverloaded when the request cannot be
// queued with overflow "reject", and ErrShuttingDown during shutdown. With
// overflow "drop" they return a nil error and report the spans in
// IngestResult.Dropped.
type Ingestor interface {
	IngestSpans(ctx context.Context, spans []RawSpan) (IngestResult, error)
	IngestLogs(ctx context.Context, records []RawLogRecord) (IngestResult, error)
	// Count records a receiver-side outcome (limit rejections, discarded metrics).
	Count(c IngestCount)
}

// Batch is one writer transaction worth of ingest data.
type Batch struct {
	Resources []Resource
	Spans     []Span
	// Content holds parts not carried by a span of this batch (GenAI log
	// events); they are stored even when their span has not arrived.
	Content []ContentPart
	Counts  []IngestCount
}

// BatchResult reports what a committed batch changed.
type BatchResult struct {
	Inserted   int // spans actually inserted
	Duplicates int // spans ignored because (trace_id, span_id) was already stored
	// UsageDuplicates counts usage_duplicate flags newly set, by kind.
	UsageDuplicates map[Kind]int
}

// SpanStore persists batches atomically: either the whole batch commits or nothing does.
// Inserts are idempotent on (trace_id, span_id); aggregates change only for inserted rows.
type SpanStore interface {
	WriteBatch(ctx context.Context, b *Batch) (BatchResult, error)
}

// Job is a unit of write work that runs on the single writer goroutine, between
// ingest batches, so that it never writes concurrently with a batch or another
// job. Retention, pricing recompute, vacuum and user deletion are jobs.
//
// Run captures the store it needs (a port declared by its own package); it should
// do a bounded amount of work, e.g. retention.batch rows in one transaction, and
// leave further work to further jobs so that ingest batches interleave. Its
// context is cancelled when the submitter gives up or the shutdown deadline passes.
type Job struct {
	// Name identifies the job kind in logs, e.g. "retention_spans"; it must come
	// from a fixed vocabulary, never from user data.
	Name string
	Run  func(ctx context.Context) error
}

// JobRunner executes jobs serially on the single writer. Implemented by app/writer.
type JobRunner interface {
	// Submit queues j, waits until it has run and returns the error of j.Run
	// (wrapped; a panic in Run is returned as an error). It returns ctx.Err()
	// when ctx ends first (a queued job is then skipped and a running one sees
	// its context cancelled) and ErrShuttingDown once the writer stops
	// accepting work; jobs still queued at shutdown are not run.
	Submit(ctx context.Context, j Job) error
}
