// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func (w *txWriter) writeTraces(ctx context.Context) error {
	for _, id := range w.order {
		st := w.traces[id]
		if !st.dirty {
			continue
		}
		if _, err := w.stmt(stUpsTrace).exec(ctx, st.row.args(id)...); err != nil {
			return fmt.Errorf("sqlite: write trace: %w", err)
		}
	}
	return nil
}

type sessionState struct {
	row   sessionRow
	dirty bool
}

func (w *txWriter) session(ctx context.Context, id string) (*sessionState, bool, error) {
	if ss, ok := w.sessions[id]; ok {
		return ss, true, nil
	}
	ss := &sessionState{}
	row, err := scanSessionRow(w.stmt(stSelSession).queryRow(ctx, id))
	exists := true
	switch {
	case err == nil:
		ss.row = row
	case errors.Is(err, sql.ErrNoRows):
		exists = false
	default:
		return nil, false, wrapScan("session", err)
	}
	w.sessions[id] = ss
	w.sessOrd = append(w.sessOrd, id)
	return ss, exists, nil
}

func (w *txWriter) writeSessions(ctx context.Context) error {
	for _, id := range w.order {
		st := w.traces[id]
		if !st.dirty || st.row.sessionID == "" {
			continue
		}
		ss, exists, err := w.session(ctx, st.row.sessionID)
		if err != nil {
			return err
		}
		r := &ss.row
		if !exists && !ss.dirty {
			r.firstNs, r.lastNs = st.row.startNs, st.row.endNs
		}
		if st.before.sessionID == "" {
			r.traceCount++
			r.usage = r.usage.Add(st.row.usage)
			r.costNanos += st.row.costNanos
		} else {
			r.usage = r.usage.Add(st.row.usage.Sub(st.before.usage))
			r.costNanos += st.row.costNanos - st.before.costNanos
		}
		r.firstNs, r.lastNs = min(r.firstNs, st.row.startNs), max(r.lastNs, st.row.endNs)
		if r.userID == "" {
			r.userID = st.row.userID
		}
		ss.dirty = true
	}
	for _, id := range w.sessOrd {
		ss := w.sessions[id]
		if !ss.dirty {
			continue
		}
		if _, err := w.stmt(stUpsSession).exec(ctx, ss.row.args(id)...); err != nil {
			return fmt.Errorf("sqlite: write session: %w", err)
		}
	}
	return nil
}

func keyArgs(k rollup.Key) []any {
	return []any{k.BucketNs, k.Service, k.Provider, k.Model, string(k.Kind)}
}

func userKeyArgs(k rollup.UserKey) []any { return []any{k.BucketNs, k.UserID} }

func measureArgs(m *rollup.Measures) []any {
	return []any{
		m.Count, m.Errors, m.Usage.Input, m.Usage.Output, m.Usage.CacheRead,
		m.Usage.CacheWrite, m.Usage.Reasoning, m.CostNanos, m.Unpriced,
	}
}

func (w *txWriter) writeRollups(ctx context.Context) error {
	d := &w.deltas
	if err := flushRollup(ctx, w.st.rollup[rtHour], d.hour, keyArgs); err != nil {
		return err
	}
	if err := flushRollup(ctx, w.st.rollup[rtDay], d.day, keyArgs); err != nil {
		return err
	}
	if err := flushRollup(ctx, w.st.rollup[rtUserHour], d.userHour, userKeyArgs); err != nil {
		return err
	}
	return flushRollup(ctx, w.st.rollup[rtUserDay], d.userDay, userKeyArgs)
}

func flushRollup[K comparable](
	ctx context.Context, st rollupStmts, m map[K]*rollup.Aggregate, args func(K) []any,
) error {
	for k, a := range m {
		key := args(k)
		vals := append(append([]any{}, key...), measureArgs(&a.Measures)...)
		if a.Histogram.IsEmpty() {
			if _, err := st.upsMeasures.exec(ctx, append(vals, emptyHistogram())...); err != nil {
				return fmt.Errorf("sqlite: write rollup: %w", err)
			}
			continue
		}
		merged, err := mergeStoredHistogram(ctx, st.selHist, key, &a.Histogram)
		if err != nil {
			return err
		}
		if _, err := st.upsHist.exec(ctx, append(vals, merged)...); err != nil {
			return fmt.Errorf("sqlite: write rollup: %w", err)
		}
	}
	return nil
}

func mergeStoredHistogram(ctx context.Context, sel stmt, key []any, add *rollup.Histogram) ([]byte, error) {
	var blob []byte
	var h rollup.Histogram
	err := sel.queryRow(ctx, key...).Scan(&blob)
	switch {
	case err == nil:
		if derr := h.UnmarshalBinary(blob); derr != nil {
			return nil, fmt.Errorf("sqlite: stored histogram: %w", derr)
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, wrapScan("rollup histogram", err)
	}
	h.Merge(add)
	out, err := h.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("sqlite: encode histogram: %w", err)
	}
	return out, nil
}

func emptyHistogram() []byte {
	var h rollup.Histogram
	b, err := h.MarshalBinary()
	if err != nil {
		return nil
	}
	return b
}

func (w *txWriter) touchTraces(ctx context.Context, ids []trace.ID) error {
	for _, id := range ids {
		st, err := w.traceState(ctx, id)
		if err != nil {
			return err
		}
		if st.exists {
			st.settle = true
		}
	}
	return nil
}
