// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type stmtID int

const (
	stInsResource stmtID = iota
	stInsSpan
	stMarkContent
	stInsContent
	stSelContentBudget
	stSelTrace
	stUpsTrace
	stSelTraceSpans
	stUpdSpanEff
	stSelSession
	stUpsSession
	stUpsIngest
	numStmts
)

const spanInsertCols = `trace_id, span_id, parent_span_id, resource_id, service, name, kind, status,
  start_ns, end_ns, duration_ns, model, provider,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, has_usage, cost_nanos,
  session_id, user_id, error_type, status_message, flags, content_stripped, rollup_user,
  eff_cache_read, eff_cache_write, eff_reasoning, eff_cost_nanos, attributes`

const traceCols = `root_name, root_service, start_ns, end_ns, duration_ns, span_count,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
  cost_nanos, unpriced, models, has_error, session_id, user_id`

const sessionCols = `user_id, first_ns, last_ns, trace_count,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_nanos`

const usageSpanCols = `id, span_id, parent_span_id, kind, status, start_ns, duration_ns,
  service, provider, model, rollup_user,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_nanos, flags,
  eff_cache_read, eff_cache_write, eff_reasoning, eff_cost_nanos`

const measureCols = `span_count, errors, input_tokens, output_tokens, cache_read_tokens,
  cache_write_tokens, reasoning_tokens, cost_nanos, unpriced`

func writeSQL() [numStmts]string {
	return [numStmts]string{
		stInsResource: `INSERT OR IGNORE INTO resources (id, attributes) VALUES (?, ?)`,
		stInsSpan: `INSERT OR IGNORE INTO spans (` + spanInsertCols + `) VALUES (` +
			placeholders(32) + `)`,
		stMarkContent: `UPDATE span_content SET has_span = 1 WHERE trace_id = ? AND span_id = ? AND has_span = 0`,
		stInsContent: `INSERT OR IGNORE INTO span_content
  (trace_id, span_id, source, time_ns, key, part, role, bytes, body, truncated, has_span, inserted_ns)
  VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10,
  EXISTS (SELECT 1 FROM spans WHERE trace_id = ?1 AND span_id = ?2), ?11)`,
		stSelContentBudget: `SELECT body, truncated FROM span_content
  WHERE trace_id = ? AND span_id = ? AND body IS NOT NULL`,
		stSelTrace:      `SELECT ` + traceCols + ` FROM traces WHERE trace_id = ?`,
		stUpsTrace:      upsertSQL("traces", "trace_id", traceCols),
		stSelTraceSpans: `SELECT ` + usageSpanCols + ` FROM spans WHERE trace_id = ?`,
		stUpdSpanEff: `UPDATE spans SET flags = ?, eff_cache_read = ?, eff_cache_write = ?, eff_reasoning = ?,
  eff_cost_nanos = ? WHERE id = ?`,
		stSelSession: `SELECT ` + sessionCols + ` FROM sessions WHERE id = ?`,
		stUpsSession: upsertSQL("sessions", "id", sessionCols),
		stUpsIngest: `INSERT INTO ingest_stats (hour_ns, signal, result, reason, n) VALUES (?, ?, ?, ?, ?)
  ON CONFLICT (hour_ns, signal, result, reason) DO UPDATE SET n = n + excluded.n`,
	}
}

func upsertSQL(table, key, cols string) string {
	names := splitCols(cols)
	set := make([]string, len(names))
	for i, c := range names {
		set[i] = c + " = excluded." + c
	}
	return `INSERT INTO ` + table + ` (` + key + `, ` + cols + `) VALUES (` + placeholders(len(names)+1) +
		`) ON CONFLICT (` + key + `) DO UPDATE SET ` + strings.Join(set, ", ")
}

func splitCols(cols string) []string {
	parts := strings.Split(cols, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

const (
	tableRollupHour     = "rollup_hour"
	tableRollupDay      = "rollup_day"
	tableRollupUserHour = "rollup_user_hour"
	tableRollupUserDay  = "rollup_user_day"
	bucketCol           = "bucket_ns"
	rollupKeyCols       = "bucket_ns, service, provider, model, kind"
	userRollupKeyCols   = "bucket_ns, user_id"
)

type rollupTable struct {
	name    string
	keyCols string
}

const (
	rtHour = iota
	rtDay
	rtUserHour
	rtUserDay
	numRollupTables
)

func rollupTables() [numRollupTables]rollupTable {
	const key, userKey = rollupKeyCols, userRollupKeyCols
	return [numRollupTables]rollupTable{
		rtHour:     {name: tableRollupHour, keyCols: key},
		rtDay:      {name: tableRollupDay, keyCols: key},
		rtUserHour: {name: tableRollupUserHour, keyCols: userKey},
		rtUserDay:  {name: tableRollupUserDay, keyCols: userKey},
	}
}

func (t rollupTable) where() string {
	names := splitCols(t.keyCols)
	conds := make([]string, len(names))
	for i, c := range names {
		conds[i] = c + " = ?"
	}
	return strings.Join(conds, " AND ")
}

type stmt struct{ s *sql.Stmt }

func (st stmt) exec(ctx context.Context, args ...any) (sql.Result, error) {
	return st.s.ExecContext(ctx, args...) //nolint:wrapcheck // callers wrap with what they wrote
}

func (st stmt) queryRow(ctx context.Context, args ...any) *sql.Row {
	return st.s.QueryRowContext(ctx, args...)
}

func (st stmt) query(ctx context.Context, args ...any) (*sql.Rows, error) {
	return st.s.QueryContext(ctx, args...) //nolint:wrapcheck // callers wrap and close
}

type rollupStmts struct {
	selHist     stmt
	upsHist     stmt
	upsMeasures stmt
}

func (t rollupTable) sql() (selHist, upsHist, upsMeasures string) {
	nKey := len(splitCols(t.keyCols))
	nMeasures := len(splitCols(measureCols))
	add := make([]string, 0, nMeasures)
	for _, c := range splitCols(measureCols) {
		add = append(add, c+" = "+c+" + excluded."+c)
	}
	insert := `INSERT INTO ` + t.name + ` (` + t.keyCols + `, ` + measureCols + `, histogram) VALUES (` +
		placeholders(nKey+nMeasures+1) + `) ON CONFLICT (` + t.keyCols + `) DO UPDATE SET ` + strings.Join(add, ", ")
	return `SELECT histogram FROM ` + t.name + ` WHERE ` + t.where(),
		insert + `, histogram = excluded.histogram`,
		insert
}

type writeStmts struct {
	st     [numStmts]stmt
	rollup [numRollupTables]rollupStmts
}

func prepareWriteStmts(ctx context.Context, db *sql.DB) (*writeStmts, error) {
	ws := &writeStmts{}
	var errs []error
	prep := func(q string) stmt {
		st, err := db.PrepareContext(ctx, q)
		if err != nil {
			errs = append(errs, fmt.Errorf("sqlite: prepare %q: %w", firstLine(q), err))
		}
		return stmt{s: st}
	}
	for i, q := range writeSQL() {
		ws.st[i] = prep(q)
	}
	for i, t := range rollupTables() {
		sel, ups, upm := t.sql()
		ws.rollup[i] = rollupStmts{selHist: prep(sel), upsHist: prep(ups), upsMeasures: prep(upm)}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, errors.Join(err, ws.close())
	}
	return ws, nil
}

func (ws *writeStmts) bind(ctx context.Context, tx *sql.Tx) *writeStmts {
	on := func(st stmt) stmt {
		return stmt{s: tx.StmtContext(ctx, st.s)}
	}
	b := &writeStmts{}
	for i, st := range ws.st {
		b.st[i] = on(st)
	}
	for i, r := range ws.rollup {
		b.rollup[i] = rollupStmts{selHist: on(r.selHist), upsHist: on(r.upsHist), upsMeasures: on(r.upsMeasures)}
	}
	return b
}

func (ws *writeStmts) close() error {
	var errs []error
	closeOne := func(st stmt) {
		if st.s != nil {
			errs = append(errs, st.s.Close())
		}
	}
	for _, st := range ws.st {
		closeOne(st)
	}
	for _, r := range ws.rollup {
		closeOne(r.selHist)
		closeOne(r.upsHist)
		closeOne(r.upsMeasures)
	}
	return errors.Join(errs...)
}

func firstLine(q string) string {
	if i := strings.IndexByte(q, '\n'); i >= 0 {
		return q[:i]
	}
	return q
}
