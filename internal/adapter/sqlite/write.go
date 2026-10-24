// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// WriteBatch writes one batch of spans in a single transaction.
func (s *Store) WriteBatch(ctx context.Context, b *trace.Batch) (trace.BatchResult, error) {
	if b == nil {
		return trace.BatchResult{}, nil
	}
	var res trace.BatchResult
	err := s.inTx(ctx, func(w *txWriter) error {
		if err := w.writeBatch(ctx, b); err != nil {
			return err
		}
		res = w.res
		return nil
	})
	if err != nil {
		return trace.BatchResult{}, err
	}
	return res, nil
}

func (s *Store) inTx(ctx context.Context, fn func(w *txWriter) error) error {
	return s.rawTx(ctx, func(tx *sql.Tx) error {
		return fn(newTxWriter(s, s.stmts.bind(ctx, tx), tx))
	})
}

func rollback(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("sqlite: rollback: %w", err)
	}
	return nil
}

type traceState struct {
	id     trace.ID
	exists bool
	before traceRow
	row    traceRow
	added  bool
	dirty  bool
	settle bool
}

type txWriter struct {
	s        *Store
	tx       *sql.Tx
	st       *writeStmts
	now      int64
	traces   map[trace.ID]*traceState
	order    []trace.ID
	newRows  map[int64]struct{}
	deltas   rollupDeltas
	sessions map[string]*sessionState
	sessOrd  []string
	budgets  map[spanKey]*content.Budget
	res      trace.BatchResult
}

type spanKey struct {
	tid trace.ID
	sid trace.SpanID
}

func newTxWriter(s *Store, st *writeStmts, tx *sql.Tx) *txWriter {
	return &txWriter{
		s: s, tx: tx, st: st, now: s.cfg.clock.Now().UnixNano(),
		traces:   make(map[trace.ID]*traceState),
		newRows:  make(map[int64]struct{}),
		deltas:   newRollupDeltas(),
		sessions: make(map[string]*sessionState),
		budgets:  make(map[spanKey]*content.Budget),
		res:      trace.BatchResult{UsageDuplicates: map[trace.Kind]int{}},
	}
}

func (w *txWriter) stmt(id stmtID) stmt { return w.st.st[id] }

func (w *txWriter) writeBatch(ctx context.Context, b *trace.Batch) error {
	if err := w.insertResources(ctx, b.Resources); err != nil {
		return err
	}
	if err := w.insertSpans(ctx, b.Spans); err != nil {
		return err
	}
	for i := range b.Content {
		if err := w.insertContent(ctx, &b.Content[i], b.Content[i].TraceID, b.Content[i].SpanID, 0); err != nil {
			return err
		}
	}
	if err := w.finish(ctx); err != nil {
		return err
	}
	return w.foldCounts(ctx, b.Counts)
}

func (w *txWriter) finish(ctx context.Context) error {
	if err := w.settleTraces(ctx); err != nil {
		return err
	}
	if err := w.writeTraces(ctx); err != nil {
		return err
	}
	if err := w.writeSessions(ctx); err != nil {
		return err
	}
	return w.writeRollups(ctx)
}

func (w *txWriter) insertResources(ctx context.Context, rs []trace.Resource) error {
	for i := range rs {
		if _, err := w.stmt(stInsResource).exec(ctx, resourceKey(rs[i].ID), string(rs[i].Attributes)); err != nil {
			return fmt.Errorf("sqlite: insert resource: %w", err)
		}
	}
	return nil
}

func (w *txWriter) traceState(ctx context.Context, id trace.ID) (*traceState, error) {
	if st, ok := w.traces[id]; ok {
		return st, nil
	}
	st := &traceState{id: id}
	row, err := scanTraceRow(w.stmt(stSelTrace).queryRow(ctx, id[:]))
	switch {
	case err == nil:
		st.exists, st.before, st.row = true, row, row
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, wrapScan("trace", err)
	}
	w.traces[id] = st
	w.order = append(w.order, id)
	return st, nil
}

func batchUsers(spans []trace.Span) map[trace.ID]string {
	out := make(map[trace.ID]string)
	for i := range spans {
		if u := spans[i].UserID; u != "" {
			if _, ok := out[spans[i].TraceID]; !ok {
				out[spans[i].TraceID] = u
			}
		}
	}
	return out
}

func (w *txWriter) insertSpans(ctx context.Context, spans []trace.Span) error {
	users := batchUsers(spans)
	for i := range spans {
		sp := &spans[i]
		st, err := w.traceState(ctx, sp.TraceID)
		if err != nil {
			return err
		}
		user := sp.UserID
		if user == "" {
			user = st.row.userID
		}
		if user == "" {
			user = users[sp.TraceID]
		}
		id, inserted, err := w.insertSpan(ctx, sp, user)
		if err != nil {
			return err
		}
		if !inserted {
			w.res.Duplicates++
			continue
		}
		w.res.Inserted++
		w.newRows[id] = struct{}{}
		st.addSpan(sp)
		if err := w.spanContent(ctx, sp); err != nil {
			return err
		}
	}
	return nil
}

func (w *txWriter) insertSpan(ctx context.Context, sp *trace.Span, rollupUser string) (int64, bool, error) {
	u := sp.Usage
	res, err := w.stmt(stInsSpan).exec(ctx,
		sp.TraceID[:], sp.SpanID[:], parentArg(sp.ParentSpanID), resourceKey(sp.ResourceID),
		sp.Service, sp.Name, string(sp.Kind), string(sp.Status),
		sp.StartNs, sp.EndNs, sp.DurationNs(), sp.Model, sp.Provider,
		u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Reasoning, boolInt(sp.HasUsage), costArg(sp.Cost),
		sp.SessionID, sp.UserID, sp.ErrorType, int64(sp.Flags&^trace.FlagUsageDuplicate),
		boolInt(sp.ContentStripped), rollupUser,
		u.CacheRead, u.CacheWrite, u.Reasoning, costArg(sp.Cost), string(sp.Attributes))
	if err != nil {
		return 0, false, fmt.Errorf("sqlite: insert span: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("sqlite: insert span: %w", err)
	}
	if n == 0 {
		return 0, false, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("sqlite: insert span: %w", err)
	}
	return id, true, nil
}

func (w *txWriter) spanContent(ctx context.Context, sp *trace.Span) error {
	if _, err := w.stmt(stMarkContent).exec(ctx, sp.TraceID[:], sp.SpanID[:]); err != nil {
		return fmt.Errorf("sqlite: attach content: %w", err)
	}
	for i := range sp.Content {
		if err := w.insertContent(ctx, &sp.Content[i], sp.TraceID, sp.SpanID, sp.StartNs); err != nil {
			return err
		}
	}
	return nil
}

func (w *txWriter) insertContent(
	ctx context.Context, p *trace.ContentPart, tid trace.ID, sid trace.SpanID, spanStart int64,
) error {
	at := p.TimeNs
	if at == 0 {
		at = spanStart
	}
	body, truncated := p.Body, p.Truncated
	var budget *content.Budget
	var next content.Budget
	if body != nil && w.s.cfg.contentMaxBytes > 0 {
		var err error
		if budget, err = w.budget(ctx, tid, sid); err != nil {
			return err
		}
		next = *budget
		var cut bool
		body, cut = next.Take(body)
		truncated = truncated || cut
		if truncated {
			next.Exhaust()
		}
	}
	res, err := w.stmt(stInsContent).exec(ctx,
		tid[:], sid[:], string(p.Source), at, p.Key, int64(p.Part), p.Role, int64(p.Bytes),
		w.s.codec.compress(body), boolInt(truncated), w.now)
	if err != nil {
		return fmt.Errorf("sqlite: insert content: %w", err)
	}
	if budget == nil {
		return nil
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: insert content: %w", err)
	}
	if n > 0 {
		*budget = next
	}
	return nil
}

func (w *txWriter) budget(ctx context.Context, tid trace.ID, sid trace.SpanID) (*content.Budget, error) {
	k := spanKey{tid: tid, sid: sid}
	if b, ok := w.budgets[k]; ok {
		return b, nil
	}
	rows, err := w.stmt(stSelContentBudget).query(ctx, tid[:], sid[:])
	if err != nil {
		return nil, fmt.Errorf("sqlite: content budget: %w", err)
	}
	defer func() { _ = rows.Close() }()
	used, exhausted := 0, false
	for rows.Next() {
		var (
			blob      []byte
			truncated bool
		)
		if err := rows.Scan(&blob, &truncated); err != nil {
			return nil, wrapScan("content budget", err)
		}
		n, err := w.s.codec.size(blob)
		if err != nil {
			return nil, err
		}
		used += n
		exhausted = exhausted || truncated
	}
	if err := rows.Err(); err != nil {
		return nil, wrapScan("content budget", err)
	}
	b := content.NewBudget(w.s.cfg.contentMaxBytes, used, exhausted)
	w.budgets[k] = &b
	return &b, nil
}

func (st *traceState) addSpan(sp *trace.Span) {
	r := &st.row
	end := sp.StartNs + sp.DurationNs()
	if r.spanCount == 0 {
		r.startNs, r.endNs = sp.StartNs, end
	} else {
		r.startNs, r.endNs = min(r.startNs, sp.StartNs), max(r.endNs, end)
	}
	r.spanCount++
	switch {
	case sp.IsRoot() && !r.rootName.Valid:
		r.rootName, r.rootService = sql.NullString{String: sp.Name, Valid: true}, sp.Service
	case !r.rootName.Valid && r.rootService == "":
		r.rootService = sp.Service
	}
	r.models = addModel(r.models, sp.Model)
	r.hasError = r.hasError || sp.Status == trace.StatusError
	if r.sessionID == "" {
		r.sessionID = sp.SessionID
	}
	if r.userID == "" {
		r.userID = sp.UserID
	}
	st.added, st.dirty, st.settle = true, true, true
}

type countKey struct {
	hour   int64
	signal trace.Signal
	result trace.Result
	reason string
}

func (w *txWriter) foldCounts(ctx context.Context, counts []trace.IngestCount) error {
	sums := make(map[countKey]int64)
	var keys []countKey
	add := func(k countKey, n int64) {
		if n == 0 {
			return
		}
		if _, ok := sums[k]; !ok {
			keys = append(keys, k)
		}
		sums[k] += n
	}
	for _, c := range counts {
		add(countKey{rollup.HourStart(c.AtNs), c.Signal, c.Result, c.Reason}, c.N)
	}
	hour := rollup.HourStart(w.now)
	add(countKey{hour, trace.SignalTraces, trace.ResultStored, ""}, int64(w.res.Inserted))
	add(countKey{hour, trace.SignalTraces, trace.ResultDuplicate, ""}, int64(w.res.Duplicates))
	for _, k := range keys {
		_, err := w.stmt(stUpsIngest).exec(ctx, k.hour, string(k.signal), string(k.result), k.reason, sums[k])
		if err != nil {
			return fmt.Errorf("sqlite: fold ingest stats: %w", err)
		}
	}
	return nil
}
