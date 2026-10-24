// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type Admin struct{ s *Store }

func (s *Store) Admin() *Admin { return &Admin{s: s} }

var _ admin.Store = (*Admin)(nil)

type recomputeRow struct {
	id      int64
	traceID trace.ID
	in      cost.Input
	cost    trace.Cost
	flags   trace.Flags
}

func (a *Admin) RecomputeCosts(
	ctx context.Context, calc *cost.Calculator, r admin.Range, afterKey int64, limit int,
) (int64, int, error) {
	if calc == nil || limit < 1 {
		return 0, 0, fmt.Errorf("%w: recompute needs a calculator and a positive limit", ErrInvalidOption)
	}
	next, n := afterKey, 0
	err := a.s.inTx(ctx, func(w *txWriter) error {
		rows, err := selectRecompute(ctx, w.tx, r, afterKey, limit)
		if err != nil {
			return err
		}
		var touched []trace.ID
		for i := range rows {
			row := &rows[i]
			next, n = row.id, n+1
			res, err := calc.Cost(row.in)
			if err != nil {
				res = cost.Result{}
			}
			flags := row.flags&^trace.FlagUsageClamped | res.Flags
			if res.Cost == row.cost && flags == row.flags {
				continue
			}
			if _, err := w.tx.ExecContext(ctx, "UPDATE spans SET cost_nanos = ?, flags = ? WHERE id = ?",
				costArg(res.Cost), int64(flags), row.id); err != nil {
				return fmt.Errorf("sqlite: update span cost: %w", err)
			}
			touched = append(touched, row.traceID)
		}
		if err := w.touchTraces(ctx, touched); err != nil {
			return err
		}
		return w.finish(ctx)
	})
	if err != nil {
		return afterKey, 0, err
	}
	return next, n, nil
}

func recomputeSQL(r admin.Range, after int64, limit int) (string, []any) {
	var w where
	w.add("id > ?", after)
	if !r.Since.IsZero() {
		w.add("start_ns >= ?", r.Since.UnixNano())
	}
	if !r.Until.IsZero() {
		w.add("start_ns < ?", r.Until.UnixNano())
	}
	q := `SELECT id, trace_id, provider, model, start_ns, ` +
		`input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_nanos, flags
		FROM spans WHERE ` + w.sql() + ` ORDER BY id LIMIT ?`
	return q, append(w.args, limit)
}

func selectRecompute(ctx context.Context, tx *sql.Tx, r admin.Range, after int64, limit int) ([]recomputeRow, error) {
	q, args := recomputeSQL(r, after, limit)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: select spans to reprice: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []recomputeRow
	for rows.Next() {
		var row recomputeRow
		var tid []byte
		var start, flags int64
		var c sql.NullInt64
		u := &row.in.Usage
		if serr := rows.Scan(&row.id, &tid, &row.in.Provider, &row.in.ResponseModel, &start,
			&u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &u.Reasoning, &c, &flags); serr != nil {
			return nil, wrapScan("span", serr)
		}
		if row.traceID, err = scanTraceID(tid); err != nil {
			return nil, err
		}
		row.in.Start = time.Unix(0, start).UTC()
		row.cost = costOf(c)
		row.flags = trace.Flags(flags) //nolint:gosec // G115: flags are a small bit set written by this package
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: select spans to reprice: %w", err)
	}
	return out, nil
}

func (a *Admin) DeleteUserData(ctx context.Context, userID string, limit int) (int, error) {
	if userID == "" || limit < 1 {
		return 0, fmt.Errorf("%w: delete user data needs a user id and a positive limit", ErrInvalidOption)
	}
	n := 0
	err := a.s.rawTx(ctx, func(tx *sql.Tx) error {
		var err error
		if n, err = deleteUserTraces(ctx, tx, userID, limit); err != nil {
			return err
		}
		for _, q := range []string{
			"DELETE FROM sessions WHERE id IN (SELECT id FROM sessions WHERE user_id = ?1 AND user_id <> '' LIMIT ?2)",
			`DELETE FROM rollup_user_hour WHERE (bucket_ns, user_id) IN
				(SELECT bucket_ns, user_id FROM rollup_user_hour WHERE user_id = ?1 LIMIT ?2)`,
			`DELETE FROM rollup_user_day WHERE (bucket_ns, user_id) IN
				(SELECT bucket_ns, user_id FROM rollup_user_day WHERE user_id = ?1 LIMIT ?2)`,
		} {
			if n >= limit {
				break
			}
			m, err := execCount(ctx, tx, q, userID, limit-n)
			if err != nil {
				return err
			}
			n += m
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete user data: %w", err)
	}
	return n, nil
}

func deleteUserTraces(ctx context.Context, tx *sql.Tx, userID string, limit int) (int, error) {
	n := 0
	for n < limit {
		tid, ok, err := userTrace(ctx, tx, userID)
		if err != nil || !ok {
			return n, err
		}
		m, err := deleteTrace(ctx, tx, tid)
		if err != nil {
			return 0, err
		}
		n += m
	}
	return n, nil
}

const userTraceSQL = `SELECT trace_id FROM traces WHERE user_id = ?1 AND user_id <> ''
	UNION ALL SELECT trace_id FROM spans WHERE user_id = ?1 AND user_id <> '' LIMIT 1`

func userTrace(ctx context.Context, tx *sql.Tx, userID string) ([]byte, bool, error) {
	var tid []byte
	err := tx.QueryRowContext(ctx, userTraceSQL, userID).Scan(&tid)
	switch {
	case err == nil:
		return tid, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, false, nil
	default:
		return nil, false, wrapScan("user trace", err)
	}
}

func deleteTrace(ctx context.Context, tx *sql.Tx, tid []byte) (int, error) {
	total := 0
	for _, q := range []string{
		"DELETE FROM span_content WHERE trace_id = ?",
		"DELETE FROM spans WHERE trace_id = ?",
		"DELETE FROM traces WHERE trace_id = ?",
	} {
		n, err := execCount(ctx, tx, q, tid)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

func (a *Admin) IncrementalVacuum(ctx context.Context, pages int) (int, error) {
	before, after, err := a.s.incrementalVacuum(ctx, pages)
	if err != nil {
		return 0, err
	}
	return before - after, nil
}

func (a *Admin) Checkpoint(ctx context.Context) error {
	_, err := a.s.Checkpoint(ctx)
	return err
}

func (s *Store) BackupTo(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("%w: empty backup path", ErrInvalidOption)
	}
	db, err := sql.Open(driverName, dsn(s.path, readOnly(), commonPragmas()))
	if err != nil {
		return fmt.Errorf("sqlite: open backup connection: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	return vacuumInto(ctx, db, path)
}
