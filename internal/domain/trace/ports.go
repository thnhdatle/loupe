// SPDX-License-Identifier: Apache-2.0

package trace

import "context"

// Ingestor accepts decoded OTLP data from the receivers.
type Ingestor interface {
	IngestSpans(ctx context.Context, spans []RawSpan) (IngestResult, error)
	IngestLogs(ctx context.Context, records []RawLogRecord) (IngestResult, error)
	Count(c IngestCount)
}

// Batch is one writer transaction worth of ingest data.
type Batch struct {
	Resources []Resource
	Spans     []Span
	Content   []ContentPart
	Counts    []IngestCount
}

type BatchResult struct {
	Inserted        int
	Duplicates      int
	UsageDuplicates map[Kind]int
}

// SpanStore persists ingest batches atomically.
type SpanStore interface {
	WriteBatch(ctx context.Context, b *Batch) (BatchResult, error)
}

type Job struct {
	Name string
	Run  func(ctx context.Context) error
}

type JobRunner interface {
	Submit(ctx context.Context, j Job) error
}
