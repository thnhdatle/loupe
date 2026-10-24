// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const spanViewCols = `s.trace_id, s.span_id, s.parent_span_id, s.resource_id, s.service, s.name, s.kind, s.status,
  s.start_ns, s.end_ns, s.model, s.provider,
  s.input_tokens, s.output_tokens, s.cache_read_tokens, s.cache_write_tokens, s.reasoning_tokens,
  s.has_usage, s.cost_nanos, s.eff_cost_nanos, s.session_id, s.user_id, s.error_type, s.flags,
  s.content_stripped, s.attributes`

type storedSpan struct {
	span    trace.Span
	effCost trace.Cost
}

func scanStoredSpan(sc scanner) (storedSpan, error) {
	var out storedSpan
	sp := &out.span
	var tid, sid, pid []byte
	var kind, status, attrs string
	var resource, flags int64
	var cost, effCost sql.NullInt64
	err := sc.Scan(&tid, &sid, &pid, &resource, &sp.Service, &sp.Name, &kind, &status,
		&sp.StartNs, &sp.EndNs, &sp.Model, &sp.Provider,
		&sp.Usage.Input, &sp.Usage.Output, &sp.Usage.CacheRead, &sp.Usage.CacheWrite, &sp.Usage.Reasoning,
		&sp.HasUsage, &cost, &effCost, &sp.SessionID, &sp.UserID, &sp.ErrorType, &flags,
		&sp.ContentStripped, &attrs)
	if err != nil {
		return storedSpan{}, wrapScan("span", err)
	}
	if sp.TraceID, err = scanTraceID(tid); err != nil {
		return storedSpan{}, err
	}
	if sp.SpanID, err = scanSpanID(sid); err != nil {
		return storedSpan{}, err
	}
	if sp.ParentSpanID, err = scanSpanID(pid); err != nil {
		return storedSpan{}, err
	}
	sp.ResourceID = uint64(resource) //nolint:gosec // G115: bit cast back from resourceKey
	sp.Kind, sp.Status = trace.Kind(kind), trace.Status(status)
	sp.Flags = trace.Flags(flags) //nolint:gosec // G115: flags are a small bit set written by this package
	sp.Cost, out.effCost = costOf(cost), costOf(effCost)
	sp.Attributes = []byte(attrs)
	return out, nil
}

func (s *storedSpan) view(includeContent bool) query.SpanView {
	v := query.NewSpanView(&s.span, includeContent)
	if !s.span.Flags.Has(trace.FlagUsageDuplicate) {
		v.CostNanos = nil
		if s.effCost.Priced {
			n := s.effCost.Nanos
			v.CostNanos = &n
		}
	}
	for i := range s.span.Content {
		if s.span.Content[i].IsMarker() {
			v.ContentStripped = true
		}
	}
	return v
}

func (s *Store) readSpans(ctx context.Context, db queryer, tail string, args ...any) ([]storedSpan, error) {
	rows, err := db.QueryContext(ctx, spanViewSQL(tail), args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query spans: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []storedSpan
	for rows.Next() {
		sp, err := scanStoredSpan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: query spans: %w", err)
	}
	return out, nil
}

func (s *Store) attachContent(ctx context.Context, db queryer, id trace.ID, spans []storedSpan, inc bool) error {
	rows, err := db.QueryContext(ctx, contentSQL(inc), id[:])
	if err != nil {
		return fmt.Errorf("sqlite: query content: %w", err)
	}
	defer func() { _ = rows.Close() }()
	idx := spanIndex(spans)
	for rows.Next() {
		p, err := s.scanPart(rows, id)
		if err != nil {
			return err
		}
		if i, ok := idx[p.SpanID]; ok {
			spans[i].span.Content = append(spans[i].span.Content, p)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: query content: %w", err)
	}
	return nil
}

func contentSQL(inc bool) string {
	body := "NULL"
	if inc {
		body = "body"
	}
	return `SELECT span_id, source, key, part, role, bytes, ` + body +
		`, body IS NULL, truncated, time_ns FROM span_content WHERE trace_id = ?
		ORDER BY span_id, time_ns, source, key, part`
}

func spanViewSQL(tail string) string { return "SELECT " + spanViewCols + " FROM spans s WHERE " + tail }

func spanIndex(spans []storedSpan) map[trace.SpanID]int {
	idx := make(map[trace.SpanID]int, len(spans))
	for i := range spans {
		idx[spans[i].span.SpanID] = i
	}
	return idx
}

func (s *Store) scanPart(sc scanner, tid trace.ID) (trace.ContentPart, error) {
	var sid, blob []byte
	var source string
	var part, n int64
	var marker bool
	p := trace.ContentPart{TraceID: tid}
	if err := sc.Scan(&sid, &source, &p.Key, &part, &p.Role, &n, &blob, &marker, &p.Truncated, &p.TimeNs); err != nil {
		return trace.ContentPart{}, wrapScan("content", err)
	}
	var err error
	if p.SpanID, err = scanSpanID(sid); err != nil {
		return trace.ContentPart{}, err
	}
	p.Source, p.Part, p.Bytes = trace.ContentSource(source), int(part), int(n)
	switch {
	case marker:
	case blob == nil:
		p.Body = []byte{}
	default:
		if p.Body, err = s.codec.decompress(blob); err != nil {
			return trace.ContentPart{}, err
		}
	}
	return p, nil
}
