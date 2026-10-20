// SPDX-License-Identifier: Apache-2.0

package query

import (
	"context"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// TraceReader reads traces. Implemented by the storage adapter, consumed by app/queries.
//
// Cursors in Page are validated by the implementation with DecodeCursor and the
// given sort and order. GetTrace returns trace.ErrNotFound for an unknown id.
type TraceReader interface {
	ListTraces(ctx context.Context, f TraceFilter, s Sort, o Order, p Page) (TracePage, error)
	GetTrace(ctx context.Context, id trace.ID, includeContent bool) (TraceDetail, error)
}

// SessionReader reads sessions. Implemented by the storage adapter, consumed by app/queries.
//
// Session lists are ordered by last activity descending with cursors encoded for
// (SortStart, OrderDesc); a session's traces are ordered by start ascending with
// cursors encoded for (SortStart, OrderAsc). GetSession returns trace.ErrNotFound
// for an unknown id.
type SessionReader interface {
	ListSessions(ctx context.Context, f SessionFilter, p Page) (SessionPage, error)
	GetSession(ctx context.Context, id string, p Page) (SessionDetail, error)
}

// StatsReader reads statistics that rollups cannot answer. Implemented by the
// storage adapter, consumed by app/queries.
type StatsReader interface {
	// TopSpans returns at most NormalizeLimit(Limit) llm spans whose start lies
	// in [From, To), ordered descending by cost (by=cost, spans flagged
	// usage_duplicate excluded) or duration, then by span id.
	TopSpans(ctx context.Context, r TopSpansRequest) ([]TopSpan, error)
	// IngestTotals returns the cumulative stored ingest_stats counters. The
	// queue and in-flight gauge fields are left zero.
	IngestTotals(ctx context.Context) (IngestStats, error)
	// IngestTotalsBetween sums the counters of the UTC hours that overlap
	// [from, to) (widened outward to whole hours). Counters not yet folded by
	// the writer are missing. The gauge fields are left zero.
	IngestTotalsBetween(ctx context.Context, from, to time.Time) (IngestStats, error)
}

// ExportRequest selects the traces of an export: those whose start lies in [From, To).
type ExportRequest struct {
	From, To time.Time
}

// TraceExporter streams complete traces for GET /v1/export/traces. Implemented
// by the storage adapter, consumed by the HTTP handler.
//
// Traces are delivered in batches ordered by (start, trace id) ascending. Each
// batch is read in its own short read transaction that is closed before fn is
// called, so a slow consumer never pins the WAL. Span content is not exported.
// ExportTraces stops at the first error from fn and returns it.
type TraceExporter interface {
	ExportTraces(ctx context.Context, r ExportRequest, fn func(batch []TraceDetail) error) error
}
