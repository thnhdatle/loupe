// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type scanner interface {
	Scan(dest ...any) error
}

type traceRow struct {
	rootName    sql.NullString
	rootService string
	startNs     int64
	endNs       int64
	spanCount   int64
	usage       trace.Usage
	costNanos   int64
	unpriced    int64
	models      []string
	hasError    bool
	sessionID   string
	userID      string
}

func (r *traceRow) durationNs() int64 { return r.endNs - r.startNs }

func scanTraceRow(sc scanner, extra ...any) (traceRow, error) {
	var r traceRow
	var duration int64
	var models string
	dest := make([]any, 0, len(extra)+17)
	dest = append(dest, extra...)
	dest = append(dest,
		&r.rootName, &r.rootService, &r.startNs, &r.endNs, &duration, &r.spanCount,
		&r.usage.Input, &r.usage.Output, &r.usage.CacheRead, &r.usage.CacheWrite, &r.usage.Reasoning,
		&r.costNanos, &r.unpriced, &models, &r.hasError, &r.sessionID, &r.userID)
	if err := sc.Scan(dest...); err != nil {
		return traceRow{}, err //nolint:wrapcheck // callers wrap and test for sql.ErrNoRows
	}
	var err error
	if r.models, err = decodeModels(models); err != nil {
		return traceRow{}, err
	}
	return r, nil
}

func (r *traceRow) args(id trace.ID) []any {
	return []any{
		id[:], r.rootName, r.rootService, r.startNs, r.endNs, r.durationNs(), r.spanCount,
		r.usage.Input, r.usage.Output, r.usage.CacheRead, r.usage.CacheWrite, r.usage.Reasoning,
		r.costNanos, r.unpriced, encodeModels(r.models), boolInt(r.hasError), r.sessionID, r.userID,
	}
}

func (r *traceRow) summary(id trace.ID) query.TraceSummary {
	models := r.models
	if models == nil {
		models = []string{}
	}
	return query.TraceSummary{
		TraceID: id.String(), RootName: r.rootName.String, Service: r.rootService,
		StartNs: r.startNs, EndNs: r.endNs, DurationNs: r.durationNs(), SpanCount: int(r.spanCount),
		Models: models, Usage: r.usage, CostNanos: r.costNanos, UnpricedSpans: int(r.unpriced),
		HasError: r.hasError, SessionID: r.sessionID, UserID: r.userID,
	}
}

type sessionRow struct {
	userID     string
	firstNs    int64
	lastNs     int64
	traceCount int64
	usage      trace.Usage
	costNanos  int64
}

func scanSessionRow(sc scanner, extra ...any) (sessionRow, error) {
	var r sessionRow
	dest := make([]any, 0, len(extra)+10)
	dest = append(dest, extra...)
	dest = append(dest, &r.userID, &r.firstNs, &r.lastNs, &r.traceCount,
		&r.usage.Input, &r.usage.Output, &r.usage.CacheRead, &r.usage.CacheWrite, &r.usage.Reasoning,
		&r.costNanos)
	if err := sc.Scan(dest...); err != nil {
		return sessionRow{}, err //nolint:wrapcheck // callers wrap and test for sql.ErrNoRows
	}
	return r, nil
}

func (r *sessionRow) args(id string) []any {
	return []any{
		id, r.userID, r.firstNs, r.lastNs, r.traceCount,
		r.usage.Input, r.usage.Output, r.usage.CacheRead, r.usage.CacheWrite, r.usage.Reasoning, r.costNanos,
	}
}

func (r *sessionRow) summary(id string) query.SessionSummary {
	return query.SessionSummary{
		ID: id, UserID: r.userID, FirstNs: r.firstNs, LastNs: r.lastNs,
		TraceCount: int(r.traceCount), Usage: r.usage, CostNanos: r.costNanos,
	}
}

func wrapScan(what string, err error) error { return fmt.Errorf("sqlite: read %s: %w", what, err) }
