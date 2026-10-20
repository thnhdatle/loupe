// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// exportPageTraces is the number of traces read per export transaction.
const exportPageTraces = 100

// ExportTraces implements query.TraceExporter. It pages through the traces
// starting in [From, To) by keyset (start_ns, trace_id); every page is read in
// its own read transaction, closed before fn runs.
func (s *Store) ExportTraces(ctx context.Context, r query.ExportRequest, fn func([]query.TraceDetail) error) error {
	var after *tracedRow
	for {
		batch, last, err := s.exportPage(ctx, r, after)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		if err := fn(batch); err != nil {
			return err
		}
		if len(batch) < exportPageTraces {
			return nil
		}
		after = last
	}
}

// exportPage reads one page of traces after the keyset position (nil for the
// first page), with their spans, in one read transaction.
func (s *Store) exportPage(ctx context.Context, r query.ExportRequest, after *tracedRow) (
	[]query.TraceDetail, *tracedRow, error,
) {
	tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var w where
	w.add("t.start_ns >= ?", r.From.UnixNano())
	w.add("t.start_ns < ?", r.To.UnixNano())
	if after != nil {
		w.add("(t.start_ns, t.trace_id) > (?, ?)", after.row.startNs, after.id[:])
	}
	q := "SELECT t.trace_id, " + prefixed("t.", traceCols) + " FROM traces t WHERE " + w.sql() +
		" ORDER BY t.start_ns, t.trace_id LIMIT ?"
	rows, err := s.queryTraces(ctx, tx, q, append(w.args, exportPageTraces)...)
	if err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	byTrace, err := s.exportSpans(ctx, tx, rows)
	if err != nil {
		return nil, nil, err
	}
	out := make([]query.TraceDetail, 0, len(rows))
	for i := range rows {
		spans := byTrace[rows[i].id]
		d := query.TraceDetail{Summary: rows[i].row.summary(rows[i].id), Spans: make([]query.SpanView, 0, len(spans))}
		for j := range spans {
			d.Spans = append(d.Spans, spans[j].view(false))
		}
		out = append(out, d)
	}
	return out, &rows[len(rows)-1], nil
}

// exportSpans reads the spans of the given traces, ordered by start within each trace.
func (s *Store) exportSpans(ctx context.Context, db queryer, rows []tracedRow) (map[trace.ID][]storedSpan, error) {
	marks := make([]string, len(rows))
	args := make([]any, len(rows))
	for i := range rows {
		marks[i] = "?"
		args[i] = rows[i].id[:]
	}
	tail := "s.trace_id IN (" + strings.Join(marks, ",") + ") ORDER BY s.start_ns, s.span_id"
	spans, err := s.readSpans(ctx, db, tail, args...)
	if err != nil {
		return nil, err
	}
	out := make(map[trace.ID][]storedSpan, len(rows))
	for i := range spans {
		id := spans[i].span.TraceID
		out[id] = append(out[id], spans[i])
	}
	return out, nil
}
