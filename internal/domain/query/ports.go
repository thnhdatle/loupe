// SPDX-License-Identifier: Apache-2.0

package query

import (
	"context"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// TraceReader reads traces.
type TraceReader interface {
	ListTraces(ctx context.Context, f TraceFilter, s Sort, o Order, p Page) (TracePage, error)
	GetTrace(ctx context.Context, id trace.ID, includeContent bool) (TraceDetail, error)
}

// SessionReader reads sessions.
type SessionReader interface {
	ListSessions(ctx context.Context, f SessionFilter, p Page) (SessionPage, error)
	GetSession(ctx context.Context, id string, p Page) (SessionDetail, error)
}

// StatsReader reads statistics that rollups cannot answer.
type StatsReader interface {
	TopSpans(ctx context.Context, r TopSpansRequest) ([]TopSpan, error)
	IngestTotals(ctx context.Context) (IngestStats, error)
	IngestTotalsBetween(ctx context.Context, from, to time.Time) (IngestStats, error)
}

type ExportRequest struct {
	From, To time.Time
}

// TraceExporter streams traces in batches for export.
type TraceExporter interface {
	ExportTraces(ctx context.Context, r ExportRequest, fn func(batch []TraceDetail) error) error
}
