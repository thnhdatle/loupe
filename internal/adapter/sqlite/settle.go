// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// spanRow is the view of a stored span that usage dedupe and the aggregate
// deltas need, including its stored contribution (flags and eff_* columns).
type spanRow struct {
	id         int64
	spanID     trace.SpanID
	parentID   trace.SpanID
	kind       trace.Kind
	status     trace.Status
	startNs    int64
	durationNs int64
	service    string
	provider   string
	model      string
	rollupUser string
	usage      trace.Usage
	cost       trace.Cost
	flags      trace.Flags
	effExtras  trace.Usage // CacheRead, CacheWrite, Reasoning only
	effCost    trace.Cost
}

func scanSpanRow(sc scanner) (spanRow, error) {
	var r spanRow
	var sid, pid []byte
	var kind, status string
	var cost, effCost sql.NullInt64
	var flags int64
	err := sc.Scan(&r.id, &sid, &pid, &kind, &status, &r.startNs, &r.durationNs,
		&r.service, &r.provider, &r.model, &r.rollupUser,
		&r.usage.Input, &r.usage.Output, &r.usage.CacheRead, &r.usage.CacheWrite, &r.usage.Reasoning,
		&cost, &flags, &r.effExtras.CacheRead, &r.effExtras.CacheWrite, &r.effExtras.Reasoning, &effCost)
	if err != nil {
		return spanRow{}, wrapScan("span", err)
	}
	if r.spanID, err = scanSpanID(sid); err != nil {
		return spanRow{}, err
	}
	if r.parentID, err = scanSpanID(pid); err != nil {
		return spanRow{}, err
	}
	r.kind, r.status = trace.Kind(kind), trace.Status(status)
	r.cost, r.effCost = costOf(cost), costOf(effCost)
	r.flags = trace.Flags(flags) //nolint:gosec // G115: flags are a small bit set written by this package
	return r, nil
}

// stored returns the contribution the span currently has in the aggregates.
func (r *spanRow) stored() semconv.Contribution {
	if r.flags.Has(trace.FlagUsageDuplicate) {
		return semconv.Contribution{Duplicate: true}
	}
	u := r.usage
	u.CacheRead, u.CacheWrite, u.Reasoning = r.effExtras.CacheRead, r.effExtras.CacheWrite, r.effExtras.Reasoning
	return semconv.Contribution{Usage: u, Cost: r.effCost, Unpriced: !r.effCost.Priced && !r.usage.IsZero()}
}

// measuresOf returns what a contribution adds to rollup, trace and session rows.
func measuresOf(c semconv.Contribution, status trace.Status) rollup.Measures {
	if c.Duplicate {
		return rollup.Measures{}
	}
	m := rollup.Measures{Count: 1, Usage: c.Usage}
	if status == trace.StatusError {
		m.Errors = 1
	}
	if c.Cost.Priced {
		m.CostNanos = c.Cost.Nanos
	}
	if c.Unpriced {
		m.Unpriced = 1
	}
	return m
}

func (w *txWriter) loadSpans(ctx context.Context, id trace.ID) ([]spanRow, error) {
	rows, err := w.stmt(stSelTraceSpans).query(ctx, id[:])
	if err != nil {
		return nil, fmt.Errorf("sqlite: load trace spans: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []spanRow
	for rows.Next() {
		r, err := scanSpanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: load trace spans: %w", err)
	}
	return out, nil
}

// settleTraces re-runs the usage-duplicate rule over every stored span of each
// trace marked for settling and applies the differences between each span's
// new and stored contribution (zero for spans inserted in this transaction)
// to its rollup rows, the trace and, through the trace, its session.
func (w *txWriter) settleTraces(ctx context.Context) error {
	for _, id := range w.order {
		st := w.traces[id]
		if !st.settle {
			continue
		}
		rows, err := w.loadSpans(ctx, id)
		if err != nil {
			return err
		}
		us := make([]semconv.UsageSpan, len(rows))
		for i := range rows {
			r := &rows[i]
			us[i] = semconv.UsageSpan{SpanID: r.spanID, ParentSpanID: r.parentID, Kind: r.kind, Usage: r.usage, Cost: r.cost}
		}
		for i, c := range semconv.Dedupe(us) {
			if err := w.applyContribution(ctx, st, &rows[i], c); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *txWriter) applyContribution(ctx context.Context, st *traceState, r *spanRow, c semconv.Contribution) error {
	old := r.stored()
	_, isNew := w.newRows[r.id]
	if c != old {
		flags := r.flags &^ trace.FlagUsageDuplicate
		if c.Duplicate {
			flags |= trace.FlagUsageDuplicate
		}
		_, err := w.stmt(stUpdSpanEff).exec(ctx, int64(flags),
			c.Usage.CacheRead, c.Usage.CacheWrite, c.Usage.Reasoning, costArg(c.Cost), r.id)
		if err != nil {
			return fmt.Errorf("sqlite: update span contribution: %w", err)
		}
		if c.Duplicate && !old.Duplicate {
			w.res.UsageDuplicates[r.kind]++
		}
	}
	var before rollup.Measures
	if !isNew {
		before = measuresOf(old, r.status)
	}
	d := measuresOf(c, r.status).Sub(before)
	if !isNew && d.IsZero() {
		return nil
	}
	w.deltas.add(r, d, isNew)
	if !d.IsZero() {
		st.row.usage = st.row.usage.Add(d.Usage)
		st.row.costNanos += d.CostNanos
		st.row.unpriced += d.Unpriced
		st.dirty = true
	}
	return nil
}

// rollupDeltas accumulates, per rollup row, the measure deltas and the
// latency histogram of inserted spans of one transaction.
type rollupDeltas struct {
	hour, day         map[rollup.Key]*rollup.Aggregate
	userHour, userDay map[rollup.UserKey]*rollup.Aggregate
}

func newRollupDeltas() rollupDeltas {
	return rollupDeltas{
		hour: map[rollup.Key]*rollup.Aggregate{}, day: map[rollup.Key]*rollup.Aggregate{},
		userHour: map[rollup.UserKey]*rollup.Aggregate{}, userDay: map[rollup.UserKey]*rollup.Aggregate{},
	}
}

func aggregateOf[K comparable](m map[K]*rollup.Aggregate, k K) *rollup.Aggregate {
	a, ok := m[k]
	if !ok {
		a = &rollup.Aggregate{}
		m[k] = a
	}
	return a
}

func (d *rollupDeltas) add(r *spanRow, m rollup.Measures, isNew bool) {
	key := func(res rollup.Resolution) rollup.Key {
		return rollup.Key{
			BucketNs: res.Start(r.startNs), Service: r.service, Provider: r.provider, Model: r.model, Kind: r.kind,
		}
	}
	aggs := [...]*rollup.Aggregate{
		aggregateOf(d.hour, key(rollup.ResolutionHour)),
		aggregateOf(d.day, key(rollup.ResolutionDay)),
		aggregateOf(d.userHour, rollup.UserKeyOf(r.rollupUser, r.startNs, rollup.ResolutionHour)),
		aggregateOf(d.userDay, rollup.UserKeyOf(r.rollupUser, r.startNs, rollup.ResolutionDay)),
	}
	for _, a := range aggs {
		a.Apply(m)
		if isNew {
			a.Histogram.Add(r.durationNs)
		}
	}
}
