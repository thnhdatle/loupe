// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func (s *Store) TopSpans(ctx context.Context, r query.TopSpansRequest) ([]query.TopSpan, error) {
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("sqlite: top spans: %w", err)
	}
	by, _ := query.ParseTopBy(string(r.By))
	limit, _ := query.NormalizeLimit(r.Limit)
	spans, err := s.readSpans(ctx, s.r, topSpansTail(by), string(trace.KindLLM), r.From.UnixNano(), r.To.UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	out := make([]query.TopSpan, 0, len(spans))
	for i := range spans {
		out = append(out, query.TopSpan{TraceID: spans[i].span.TraceID.String(), Span: spans[i].view(false)})
	}
	return out, nil
}

func topSpansTail(by query.TopBy) string {
	tail := "s.kind = ? AND s.start_ns >= ? AND s.start_ns < ? "
	if by == query.TopByCost {
		return tail + fmt.Sprintf("AND s.flags & %d = 0 AND s.eff_cost_nanos IS NOT NULL ", trace.FlagUsageDuplicate) +
			"ORDER BY s.eff_cost_nanos DESC, s.span_id, s.trace_id LIMIT ?"
	}
	return tail + "ORDER BY s.duration_ns DESC, s.span_id, s.trace_id LIMIT ?"
}

const ingestTotalsSQL = "SELECT signal, result, reason, SUM(n) FROM ingest_stats GROUP BY signal, result, reason"

const ingestBetweenSQL = "SELECT signal, result, reason, SUM(n) FROM ingest_stats " +
	"WHERE hour_ns >= ? AND hour_ns < ? GROUP BY signal, result, reason"

func (s *Store) IngestTotals(ctx context.Context) (query.IngestStats, error) {
	return s.queryIngest(ctx, ingestTotalsSQL)
}

func (s *Store) IngestTotalsBetween(ctx context.Context, from, to time.Time) (query.IngestStats, error) {
	if !to.After(from) {
		return query.IngestStats{}, fmt.Errorf("sqlite: ingest stats: %w", query.ErrInvalidRange)
	}
	lo := rollup.HourStart(from.UnixNano())
	hi := rollup.HourStart(to.UnixNano() - 1)
	return s.queryIngest(ctx, ingestBetweenSQL, lo, hi+int64(time.Hour))
}

func (s *Store) queryIngest(ctx context.Context, q string, args ...any) (query.IngestStats, error) {
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return query.IngestStats{}, fmt.Errorf("sqlite: query ingest stats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	st := query.IngestStats{Rejected: map[string]int64{}, Dropped: map[string]int64{}, Discarded: map[string]int64{}}
	for rows.Next() {
		var signal, result, reason string
		var n int64
		if err := rows.Scan(&signal, &result, &reason, &n); err != nil {
			return query.IngestStats{}, wrapScan("ingest stats", err)
		}
		addIngest(&st, trace.Signal(signal), trace.Result(result), reason, n)
	}
	if err := rows.Err(); err != nil {
		return query.IngestStats{}, fmt.Errorf("sqlite: query ingest stats: %w", err)
	}
	return st, nil
}

func addIngest(st *query.IngestStats, sig trace.Signal, res trace.Result, reason string, n int64) {
	if res == trace.ResultDiscarded {
		st.Discarded[string(sig)] += n
		return
	}
	if sig != trace.SignalTraces {
		return
	}
	switch res {
	case trace.ResultReceived:
		st.Received += n
	case trace.ResultStored:
		st.Stored += n
	case trace.ResultDuplicate:
		st.Duplicates += n
	case trace.ResultRejected:
		st.Rejected[reason] += n
	case trace.ResultDropped:
		st.Dropped[reason] += n
	case trace.ResultContentStripped:
		st.ContentStripped += n
	case trace.ResultUnpriced:
		st.Unpriced += n
	case trace.ResultDiscarded:
	}
}

func rollupTableName(res rollup.Resolution, user bool) (string, error) {
	switch {
	case res == rollup.ResolutionHour && !user:
		return tableRollupHour, nil
	case res == rollup.ResolutionDay && !user:
		return tableRollupDay, nil
	case res == rollup.ResolutionHour:
		return tableRollupUserHour, nil
	case res == rollup.ResolutionDay:
		return tableRollupUserDay, nil
	default:
		return "", fmt.Errorf("%w: resolution %d", rollup.ErrInvalidRange, res)
	}
}

func (s *Store) ReadRollups(
	ctx context.Context, res rollup.Resolution, r rollup.Range, f rollup.Filter,
) ([]rollup.Row, error) {
	table, err := rollupTableName(res, false)
	if err != nil {
		return nil, err
	}
	q, args := rollupsSQL(table, r, f)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []rollup.Row
	for rows.Next() {
		var row rollup.Row
		var kind string
		k := &row.Key
		err := scanAggregate(rows, &row.Aggregate, &k.BucketNs, &k.Service, &k.Provider, &k.Model, &kind)
		if err != nil {
			return nil, err
		}
		k.Kind = trace.Kind(kind)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: query %s: %w", table, err)
	}
	return out, nil
}

func rollupsSQL(table string, r rollup.Range, f rollup.Filter) (string, []any) {
	var w where
	w.add("bucket_ns >= ? AND bucket_ns < ?", r.FromNs, r.ToNs)
	if f.Kind != "" {
		w.add("kind = ?", string(f.Kind))
	}
	q := "SELECT " + rollupKeyCols + ", " + measureCols + ", histogram FROM " +
		table + " WHERE " + w.sql()
	return q, w.args
}

func userRollupsSQL(table string) string {
	return "SELECT " + userRollupKeyCols + ", " + measureCols + ", histogram FROM " +
		table + " WHERE bucket_ns >= ? AND bucket_ns < ?"
}

func (s *Store) ReadUserRollups(ctx context.Context, res rollup.Resolution, r rollup.Range) ([]rollup.UserRow, error) {
	table, err := rollupTableName(res, true)
	if err != nil {
		return nil, err
	}
	rows, err := s.r.QueryContext(ctx, userRollupsSQL(table), r.FromNs, r.ToNs)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []rollup.UserRow
	for rows.Next() {
		var row rollup.UserRow
		if err := scanAggregate(rows, &row.Aggregate, &row.Key.BucketNs, &row.Key.UserID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: query %s: %w", table, err)
	}
	return out, nil
}

func scanAggregate(sc scanner, a *rollup.Aggregate, key ...any) error {
	m := &a.Measures
	var blob []byte
	dest := make([]any, 0, len(key)+10)
	dest = append(dest, key...)
	dest = append(dest, &m.Count, &m.Errors, &m.Usage.Input, &m.Usage.Output, &m.Usage.CacheRead,
		&m.Usage.CacheWrite, &m.Usage.Reasoning, &m.CostNanos, &m.Unpriced, &blob)
	if err := sc.Scan(dest...); err != nil {
		return wrapScan("rollup", err)
	}
	if err := a.Histogram.UnmarshalBinary(blob); err != nil {
		return fmt.Errorf("sqlite: stored histogram: %w", err)
	}
	return nil
}
