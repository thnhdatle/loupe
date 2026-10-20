// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// ListSessions implements query.SessionReader: sessions whose last activity
// lies in [From, To), most recent first, keyset-paged on (last_ns, id).
func (s *Store) ListSessions(ctx context.Context, f query.SessionFilter, p query.Page) (query.SessionPage, error) {
	q, args, limit, err := listSessionsSQL(f, p)
	if err != nil {
		return query.SessionPage{}, err
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return query.SessionPage{}, fmt.Errorf("sqlite: query sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	page := query.SessionPage{Data: []query.SessionSummary{}}
	for rows.Next() {
		var id string
		r, err := scanSessionRow(rows, &id)
		if err != nil {
			return query.SessionPage{}, wrapScan("session", err)
		}
		page.Data = append(page.Data, r.summary(id))
	}
	if err := rows.Err(); err != nil {
		return query.SessionPage{}, fmt.Errorf("sqlite: query sessions: %w", err)
	}
	if len(page.Data) > limit {
		last := page.Data[limit-1]
		page.NextCursor = query.EncodeCursor(query.Cursor{SortKey: last.LastNs, ID: last.ID},
			query.SortStart, query.OrderDesc)
		page.Data = page.Data[:limit]
	}
	return page, nil
}

// listSessionsSQL builds the session-list query (limit+1 rows).
func listSessionsSQL(f query.SessionFilter, p query.Page) (string, []any, int, error) {
	if !f.From.IsZero() && !f.To.IsZero() && !f.From.Before(f.To) {
		return "", nil, 0, fmt.Errorf("%w: from is not before to", query.ErrInvalidRange)
	}
	limit, err := query.NormalizeLimit(p.Limit)
	if err != nil {
		return "", nil, 0, fmt.Errorf("sqlite: list sessions: %w", err)
	}
	var w where
	if !f.From.IsZero() {
		w.add("last_ns >= ?", f.From.UnixNano())
	}
	if !f.To.IsZero() {
		w.add("last_ns < ?", f.To.UnixNano())
	}
	if f.UserID != "" {
		w.add("user_id = ? AND user_id <> ''", f.UserID)
	}
	if p.Cursor != "" {
		c, err := query.DecodeCursor(p.Cursor, query.SortStart, query.OrderDesc)
		if err != nil {
			return "", nil, 0, fmt.Errorf("sqlite: cursor: %w", err)
		}
		w.add("(last_ns, id) < (?, ?)", c.SortKey, c.ID)
	}
	q := "SELECT id, " + sessionCols + " FROM sessions WHERE " + w.sql() +
		" ORDER BY last_ns DESC, id DESC LIMIT ?"
	return q, append(w.args, limit+1), limit, nil
}

// getSessionSQL reads one session row.
const getSessionSQL = "SELECT " + sessionCols + " FROM sessions WHERE id = ?"

// sessionTracesSQL builds the query of one page of a session's traces.
func sessionTracesSQL(id string, p query.Page) (string, []any, int, error) {
	limit, err := query.NormalizeLimit(p.Limit)
	if err != nil {
		return "", nil, 0, fmt.Errorf("sqlite: get session: %w", err)
	}
	var w where
	w.add("t.session_id = ? AND t.session_id <> ''", id)
	if p.Cursor != "" {
		if err = traceKeyset(&w, "start_ns", p.Cursor, query.SortStart, query.OrderAsc); err != nil {
			return "", nil, 0, err
		}
	}
	q := "SELECT t.trace_id, " + prefixed("t.", traceCols) + " FROM traces t WHERE " + w.sql() +
		" ORDER BY t.start_ns ASC, t.trace_id ASC LIMIT ?"
	return q, append(w.args, limit+1), limit, nil
}

// GetSession implements query.SessionReader: the session and one page of its
// traces by start ascending, read in one read transaction.
func (s *Store) GetSession(ctx context.Context, id string, p query.Page) (query.SessionDetail, error) {
	q, args, limit, err := sessionTracesSQL(id, p)
	if err != nil {
		return query.SessionDetail{}, err
	}
	tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return query.SessionDetail{}, fmt.Errorf("sqlite: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := scanSessionRow(tx.QueryRowContext(ctx, getSessionSQL, id))
	if errors.Is(err, sql.ErrNoRows) {
		return query.SessionDetail{}, fmt.Errorf("%w: session %q", trace.ErrNotFound, id)
	}
	if err != nil {
		return query.SessionDetail{}, wrapScan("session", err)
	}
	items, err := s.queryTraces(ctx, tx, q, args...)
	if err != nil {
		return query.SessionDetail{}, err
	}
	page := tracePage(items, limit, query.SortStart, query.OrderAsc)
	return query.SessionDetail{Summary: row.summary(id), Data: page.Data, NextCursor: page.NextCursor}, nil
}
