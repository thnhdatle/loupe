// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
)

// rawTx runs fn in a writer transaction and commits it.
func (s *Store) rawTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = rollback(tx)
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}

// Row selections of the retention classes; each takes (cutoff, limit).
const (
	oldContentSQL    = `SELECT id FROM span_content WHERE time_ns < ? ORDER BY time_ns LIMIT ?`
	orphanContentSQL = `SELECT c.id FROM span_content c WHERE c.has_span = 0 AND c.inserted_ns < ?
		AND NOT EXISTS (SELECT 1 FROM spans s WHERE s.trace_id = c.trace_id AND s.span_id = c.span_id)
		ORDER BY c.inserted_ns LIMIT ?`
	oldSpansSQL      = `SELECT id, trace_id, span_id FROM spans WHERE start_ns < ? ORDER BY start_ns LIMIT ?`
	emptySessionsSQL = `SELECT s.id FROM sessions s WHERE s.last_ns < ?
		AND NOT EXISTS (SELECT 1 FROM traces t WHERE t.session_id = s.id AND t.session_id <> '')
		ORDER BY s.last_ns LIMIT ?`
	deleteSpanContentSQL = "DELETE FROM span_content WHERE trace_id = ? AND span_id = ?"
	deleteEmptyTraceSQL  = `DELETE FROM traces WHERE trace_id = ?1
		AND NOT EXISTS (SELECT 1 FROM spans WHERE trace_id = ?1)`
)

// Purge implements retention.Store. The orphan-content rule ages rows by
// their insert time (inserted_ns, store clock) rather than their event time,
// so log-event content whose span arrives late is not deleted early.
func (s *Store) Purge(ctx context.Context, c retention.Class, cutoff time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, fmt.Errorf("%w: purge limit %d", ErrInvalidOption, limit)
	}
	cut := cutoff.UnixNano()
	var n int
	err := s.rawTx(ctx, func(tx *sql.Tx) error {
		var err error
		switch c {
		case retention.ClassContent:
			n, err = execCount(ctx, tx, "DELETE FROM span_content WHERE id IN ("+oldContentSQL+")", cut, limit)
		case retention.ClassOrphanContent:
			n, err = execCount(ctx, tx, "DELETE FROM span_content WHERE id IN ("+orphanContentSQL+")", cut, limit)
		case retention.ClassSpans:
			n, err = purgeSpans(ctx, tx, cut, limit)
		case retention.ClassSessions:
			n, err = execCount(ctx, tx, "DELETE FROM sessions WHERE id IN ("+emptySessionsSQL+")", cut, limit)
		case retention.ClassRollupsHourly:
			n, err = purgeBuckets(ctx, tx, limit, []bucketTable{
				{tableRollupHour, bucketCol, rollupKeyCols, cut - rollup.HourNs},
				{tableRollupUserHour, bucketCol, userRollupKeyCols, cut - rollup.HourNs},
			})
		case retention.ClassRollupsDaily:
			n, err = purgeBuckets(ctx, tx, limit, []bucketTable{
				{tableRollupDay, bucketCol, rollupKeyCols, cut - rollup.DayNs},
				{tableRollupUserDay, bucketCol, userRollupKeyCols, cut - rollup.DayNs},
				{"ingest_stats", "hour_ns", "hour_ns, signal, result, reason", cut - rollup.HourNs},
			})
		default:
			err = fmt.Errorf("%w: %q", ErrUnknownClass, c)
		}
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge %s: %w", c, err)
	}
	return n, nil
}

func execCount(ctx context.Context, tx *sql.Tx, q string, args ...any) (int, error) {
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete: %w", err)
	}
	return int(n), nil
}

// bucketTable is a table purged by bucket start: rows with bucket ≤ last.
type bucketTable struct {
	name, bucketCol, keyCols string
	last                     int64
}

// selectSQL selects the keys of the oldest rows with bucket ≤ ? (limit ?).
func (t bucketTable) selectSQL() string {
	return "SELECT " + t.keyCols + " FROM " + t.name + " WHERE " + t.bucketCol + " <= ? ORDER BY " + t.bucketCol +
		" LIMIT ?"
}

func (t bucketTable) deleteSQL() string {
	return "DELETE FROM " + t.name + " WHERE (" + t.keyCols + ") IN (" + t.selectSQL() + ")"
}

// purgeBuckets deletes the oldest qualifying rows of each table in turn,
// sharing limit across them.
func purgeBuckets(ctx context.Context, tx *sql.Tx, limit int, tables []bucketTable) (int, error) {
	total := 0
	for _, t := range tables {
		if total >= limit {
			break
		}
		n, err := execCount(ctx, tx, t.deleteSQL(), t.last, limit-total)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

type spanRef struct {
	id      int64
	traceID []byte
	spanID  []byte
}

// purgeSpans deletes the oldest spans started before cut, their content and
// the trace rows left without spans. Only spans count towards limit.
func purgeSpans(ctx context.Context, tx *sql.Tx, cut int64, limit int) (int, error) {
	refs, err := oldestSpans(ctx, tx, cut, limit)
	if err != nil {
		return 0, err
	}
	traces := map[string][]byte{}
	for _, r := range refs {
		if _, err := tx.ExecContext(ctx, deleteSpanContentSQL, r.traceID, r.spanID); err != nil {
			return 0, fmt.Errorf("sqlite: delete content: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM spans WHERE id = ?", r.id); err != nil {
			return 0, fmt.Errorf("sqlite: delete span: %w", err)
		}
		traces[string(r.traceID)] = r.traceID
	}
	for _, tid := range traces {
		if _, err := tx.ExecContext(ctx, deleteEmptyTraceSQL, tid); err != nil {
			return 0, fmt.Errorf("sqlite: delete trace: %w", err)
		}
	}
	return len(refs), nil
}

func oldestSpans(ctx context.Context, tx *sql.Tx, cut int64, limit int) ([]spanRef, error) {
	rows, err := tx.QueryContext(ctx, oldSpansSQL, cut, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: select spans: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refs []spanRef
	for rows.Next() {
		var r spanRef
		if err := rows.Scan(&r.id, &r.traceID, &r.spanID); err != nil {
			return nil, wrapScan("span", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: select spans: %w", err)
	}
	return refs, nil
}

// Size implements retention.Store: main file + WAL + SHM, missing files count 0.
func (s *Store) Size(context.Context) (int64, error) {
	var total int64
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		n, err := fileSize(p)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// FreeDisk returns the bytes available on the file system holding the database
// (a retention.FreeDiskFunc).
func (s *Store) FreeDisk(context.Context) (int64, error) {
	return freeDiskBytes(s.path)
}

// IncrementalVacuum implements retention.Store: it releases at most maxPages
// free pages and returns the free pages left.
func (s *Store) IncrementalVacuum(ctx context.Context, maxPages int) (int, error) {
	_, after, err := s.incrementalVacuum(ctx, maxPages)
	return after, err
}

func (s *Store) incrementalVacuum(ctx context.Context, maxPages int) (before, after int, err error) {
	if maxPages < 1 {
		return 0, 0, fmt.Errorf("%w: vacuum pages %d", ErrInvalidOption, maxPages)
	}
	if err := s.w.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&before); err != nil {
		return 0, 0, fmt.Errorf("sqlite: freelist_count: %w", err)
	}
	if _, err := s.w.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", maxPages)); err != nil {
		return 0, 0, fmt.Errorf("sqlite: incremental_vacuum: %w", err)
	}
	if err := s.w.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&after); err != nil {
		return 0, 0, fmt.Errorf("sqlite: freelist_count: %w", err)
	}
	return before, after, nil
}

// Checkpoint implements retention.Store: wal_checkpoint(TRUNCATE); busy
// reports that a reader kept it from completing.
func (s *Store) Checkpoint(ctx context.Context) (bool, error) {
	var busy, logPages, done int
	if err := s.w.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logPages, &done); err != nil {
		return false, fmt.Errorf("sqlite: wal_checkpoint: %w", err)
	}
	return busy != 0, nil
}
