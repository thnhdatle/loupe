// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// sortIndexMinCoverage is the share of the stored traces' time span a list
// range must cover for a cost or duration sort to walk the sort index (see
// sortIndexPlan).
const sortIndexMinCoverage = 0.25

// traceBoundsSQL reads the oldest and newest trace start; each subquery is a
// single traces_start endpoint lookup.
const traceBoundsSQL = "SELECT (SELECT min(start_ns) FROM traces), (SELECT max(start_ns) FROM traces)"

// sortIndex is the index ordered by a non-start sort column ("" for start,
// which the range on traces_start already serves).
func sortIndex(s query.Sort) string {
	switch s {
	case query.SortCost:
		return "traces_cost"
	case query.SortDuration:
		return "traces_duration"
	case query.SortStart:
		return ""
	default:
		return ""
	}
}

// sortIndexPlan reports whether a cost or duration sort should walk the sort
// index (traces_cost, traces_duration) in order, checking the time range per
// row and stopping after limit+1 matches, instead of reading every trace of
// the range from traces_start and sorting them. Without statistics the
// planner always picks the latter, which reads the whole range (benchmark query q4: all
// 100k traces of 30 days). Walking wins when the range covers a large share
// of the stored traces: about limit/coverage rows are visited. Session and
// user filters keep their own indexes.
func (s *Store) sortIndexPlan(ctx context.Context, f *query.TraceFilter, srt query.Sort) (bool, error) {
	if sortIndex(srt) == "" || f.SessionID != "" || f.UserID != "" {
		return false, nil
	}
	var lo, hi sql.NullInt64
	if err := s.r.QueryRowContext(ctx, traceBoundsSQL).Scan(&lo, &hi); err != nil {
		return false, fmt.Errorf("sqlite: trace bounds: %w", err)
	}
	if !lo.Valid || !hi.Valid {
		return false, nil
	}
	return rangeCoverage(f.From, f.To, lo.Int64, hi.Int64) >= sortIndexMinCoverage, nil
}

// rangeCoverage returns the share of [lo, hi] that [from, to) covers; a zero
// bound is open.
func rangeCoverage(from, to time.Time, lo, hi int64) float64 {
	a, b := lo, hi+1
	if !from.IsZero() {
		a = max(a, from.UnixNano())
	}
	if !to.IsZero() {
		b = min(b, to.UnixNano())
	}
	if b <= a {
		return 0
	}
	return float64(b-a) / float64(hi-lo+1)
}

// sortColumn maps a sort key to its traces column (a fixed identifier, never user input).
func sortColumn(s query.Sort) (string, error) {
	switch s {
	case query.SortStart:
		return "start_ns", nil
	case query.SortCost:
		return "cost_nanos", nil
	case query.SortDuration:
		return "duration_ns", nil
	default:
		return "", fmt.Errorf("%w: unknown sort %q", query.ErrInvalidSort, s)
	}
}

func (r *traceRow) sortKey(s query.Sort) int64 {
	switch s {
	case query.SortCost:
		return r.costNanos
	case query.SortDuration:
		return r.durationNs()
	case query.SortStart:
		return r.startNs
	default:
		return r.startNs
	}
}

// where collects SQL conditions and their arguments.
type where struct {
	conds []string
	args  []any
}

func (w *where) add(cond string, args ...any) {
	w.conds = append(w.conds, cond)
	w.args = append(w.args, args...)
}

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return "1"
	}
	return strings.Join(w.conds, " AND ")
}

// prefixUpper returns the smallest string greater than every string with
// prefix p, and false when there is none (p is all 0xff bytes).
func prefixUpper(p string) (string, bool) {
	b := []byte(p)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

// spanExists returns the EXISTS condition requiring one span of the trace to
// match every set field of f.
func spanExists(f query.SpanFilter) (string, []any) {
	var w where
	w.add("s.trace_id = t.trace_id")
	if f.Service != "" {
		w.add("s.service = ?", f.Service)
	}
	if f.Model != "" {
		w.add("s.model = ?", f.Model)
	}
	if f.Provider != "" {
		w.add("s.provider = ?", f.Provider)
	}
	if f.Kind != "" {
		w.add("s.kind = ?", string(f.Kind))
	}
	if f.Status != "" {
		w.add("s.status = ?", string(f.Status))
	}
	if f.MinDurationNs > 0 {
		w.add("s.duration_ns >= ?", f.MinDurationNs)
	}
	if f.NamePrefix != "" {
		w.add("s.name >= ?", f.NamePrefix)
		if hi, ok := prefixUpper(f.NamePrefix); ok {
			w.add("s.name < ?", hi)
		}
	}
	return "EXISTS (SELECT 1 FROM spans s WHERE " + w.sql() + ")", w.args
}

func traceWhere(f *query.TraceFilter) where {
	var w where
	if !f.From.IsZero() {
		w.add("t.start_ns >= ?", f.From.UnixNano())
	}
	if !f.To.IsZero() {
		w.add("t.start_ns < ?", f.To.UnixNano())
	}
	if f.SessionID != "" {
		w.add("t.session_id = ? AND t.session_id <> ''", f.SessionID)
	}
	if f.UserID != "" {
		w.add("t.user_id = ?", f.UserID)
	}
	if f.MinCostNanos != nil {
		w.add("t.cost_nanos >= ?", *f.MinCostNanos)
	}
	if f.HasError != nil {
		w.add("t.has_error = ?", boolInt(*f.HasError))
	}
	if !f.Span.IsEmpty() {
		impliedTraceConds(&w, f.Span)
		cond, args := spanExists(f.Span)
		w.add(cond, args...)
	}
	return w
}

// impliedTraceConds adds trace-level conditions that every trace with a span
// matching f satisfies, so the span probe runs only for candidate traces: a
// span with status error sets traces.has_error, and a trace lasts at least as
// long as each of its spans (min start to max end). Neither changes the
// result; they only skip probes (LPE-04 benchmark queries q2, q3).
func impliedTraceConds(w *where, f query.SpanFilter) {
	if f.Status == trace.StatusError {
		w.add("t.has_error = 1")
	}
	if f.MinDurationNs > 0 {
		w.add("t.duration_ns >= ?", f.MinDurationNs)
	}
}

// traceKeyset decodes a trace-list cursor and appends the condition selecting
// rows after it in the given order.
func traceKeyset(w *where, col, cursor string, srt query.Sort, o query.Order) error {
	c, err := query.DecodeCursor(cursor, srt, o)
	if err != nil {
		return fmt.Errorf("sqlite: cursor: %w", err)
	}
	id, err := c.TraceID()
	if err != nil {
		return fmt.Errorf("sqlite: cursor: %w", err)
	}
	op := "<"
	if o == query.OrderAsc {
		op = ">"
	}
	w.add("(t."+col+", t.trace_id) "+op+" (?, ?)", c.SortKey, id[:])
	return nil
}

func direction(o query.Order) (string, error) {
	switch o {
	case query.OrderDesc:
		return "DESC", nil
	case query.OrderAsc:
		return "ASC", nil
	default:
		return "", fmt.Errorf("%w: unknown order %q", query.ErrInvalidSort, o)
	}
}

// ListTraces implements query.TraceReader.
func (s *Store) ListTraces(
	ctx context.Context, f query.TraceFilter, srt query.Sort, o query.Order, p query.Page,
) (query.TracePage, error) {
	walk, err := s.sortIndexPlan(ctx, &f, srt)
	if err != nil {
		return query.TracePage{}, err
	}
	q, args, limit, err := listTracesSQL(&f, srt, o, p, walk)
	if err != nil {
		return query.TracePage{}, err
	}
	items, err := s.queryTraces(ctx, s.r, q, args...)
	if err != nil {
		return query.TracePage{}, err
	}
	return tracePage(items, limit, srt, o), nil
}

// listTracesSQL builds the trace-list query; it fetches limit+1 rows to know
// whether a next page exists. walkSort forces the sort column's index
// (sortIndexPlan); it is ignored for the start sort.
func listTracesSQL(
	f *query.TraceFilter, srt query.Sort, o query.Order, p query.Page, walkSort bool,
) (string, []any, int, error) {
	if err := f.Validate(); err != nil {
		return "", nil, 0, fmt.Errorf("sqlite: list traces: %w", err)
	}
	limit, err := query.NormalizeLimit(p.Limit)
	if err != nil {
		return "", nil, 0, fmt.Errorf("sqlite: list traces: %w", err)
	}
	col, err := sortColumn(srt)
	if err != nil {
		return "", nil, 0, err
	}
	dir, err := direction(o)
	if err != nil {
		return "", nil, 0, err
	}
	w := traceWhere(f)
	if p.Cursor != "" {
		if err = traceKeyset(&w, col, p.Cursor, srt, o); err != nil {
			return "", nil, 0, err
		}
	}
	from := "traces t"
	if idx := sortIndex(srt); walkSort && idx != "" {
		from += " INDEXED BY " + idx
	}
	q := "SELECT t.trace_id, " + prefixed("t.", traceCols) + " FROM " + from + " WHERE " + w.sql() +
		" ORDER BY t." + col + " " + dir + ", t.trace_id " + dir + " LIMIT ?"
	return q, append(w.args, limit+1), limit, nil
}

// Queries of GetTrace.
const (
	getTraceSQL    = "SELECT " + traceCols + " FROM traces WHERE trace_id = ?"
	traceSpansTail = "s.trace_id = ? ORDER BY s.start_ns, s.span_id"
)

// tracedRow is a trace row with its id.
type tracedRow struct {
	id  trace.ID
	row traceRow
}

func tracePage(items []tracedRow, limit int, srt query.Sort, o query.Order) query.TracePage {
	page := query.TracePage{Data: make([]query.TraceSummary, 0, min(len(items), limit))}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = query.EncodeCursor(query.TraceCursor(last.row.sortKey(srt), last.id), srt, o)
		items = items[:limit]
	}
	for i := range items {
		page.Data = append(page.Data, items[i].row.summary(items[i].id))
	}
	return page
}

// queryer is a *sql.DB or *sql.Tx.
type queryer interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func (s *Store) queryTraces(ctx context.Context, db queryer, q string, args ...any) ([]tracedRow, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query traces: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []tracedRow
	for rows.Next() {
		var raw []byte
		row, err := scanTraceRow(rows, &raw)
		if err != nil {
			return nil, wrapScan("trace", err)
		}
		id, err := scanTraceID(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, tracedRow{id: id, row: row})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: query traces: %w", err)
	}
	return out, nil
}

// prefixed qualifies every column of a column list with p.
func prefixed(p, cols string) string {
	names := splitCols(cols)
	for i := range names {
		names[i] = p + names[i]
	}
	return strings.Join(names, ", ")
}

// GetTrace implements query.TraceReader. The trace row, its spans and its
// content are read in one read transaction, so they are consistent.
func (s *Store) GetTrace(ctx context.Context, id trace.ID, includeContent bool) (query.TraceDetail, error) {
	tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return query.TraceDetail{}, fmt.Errorf("sqlite: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := scanTraceRow(tx.QueryRowContext(ctx, getTraceSQL, id[:]))
	if errors.Is(err, sql.ErrNoRows) {
		return query.TraceDetail{}, fmt.Errorf("%w: trace %s", trace.ErrNotFound, id)
	}
	if err != nil {
		return query.TraceDetail{}, wrapScan("trace", err)
	}
	spans, err := s.readSpans(ctx, tx, traceSpansTail, id[:])
	if err != nil {
		return query.TraceDetail{}, err
	}
	if err := s.attachContent(ctx, tx, id, spans, includeContent); err != nil {
		return query.TraceDetail{}, err
	}
	d := query.TraceDetail{Summary: row.summary(id), Spans: make([]query.SpanView, 0, len(spans))}
	for i := range spans {
		d.Spans = append(d.Spans, spans[i].view(includeContent))
	}
	return d, nil
}
