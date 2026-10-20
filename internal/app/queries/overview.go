// SPDX-License-Identifier: Apache-2.0

package queries

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// point is a (bucket, group) cell of the series.
type point struct {
	bucket int64
	key    string
}

// plan is the effective, validated overview request.
type plan struct {
	req      query.OverviewRequest
	rng      rollup.Range
	rollPlan rollup.Plan
}

// Overview returns totals, per-group stats and a time series for the range.
// Zero From and To mean the last 24 hours; the range is rounded outward to
// whole hours and returned as the effective range. Interval day reads day rows
// for whole UTC days and hour rows for the edge hours only; interval hour reads
// hour rows. Ranges above MaxHourlyRange (hour) or MaxDailyRange (day) are
// rejected with query.ErrInvalidRange. Kind is ignored for group_by=user
// because user rollups have no kind dimension.
func (s *Service) Overview(ctx context.Context, req query.OverviewRequest) (Overview, error) {
	p, err := s.planOverview(req)
	if err != nil {
		return Overview{}, err
	}
	var cells map[point]*rollup.Aggregate
	if p.req.GroupBy == query.GroupUser {
		cells, err = s.userCells(ctx, p)
	} else {
		cells, err = s.keyCells(ctx, p)
	}
	if err != nil {
		return Overview{}, err
	}
	return buildOverview(p, cells)
}

// ModelStats returns per-model totals of llm spans in the range (dashboard
// Models page), ranked by cost descending.
func (s *Service) ModelStats(ctx context.Context, from, to time.Time) ([]GroupStats, error) {
	ov, err := s.Overview(ctx, query.OverviewRequest{
		From: from, To: to, GroupBy: query.GroupModel, Interval: query.IntervalDay, Kind: trace.KindLLM,
	})
	return ov.Groups, err
}

// UserStats returns per-user totals for the range: the top 50 users by cost
// plus an "other" entry.
func (s *Service) UserStats(ctx context.Context, from, to time.Time) ([]GroupStats, error) {
	ov, err := s.Overview(ctx, query.OverviewRequest{
		From: from, To: to, GroupBy: query.GroupUser, Interval: query.IntervalDay,
	})
	return ov.Groups, err
}

func (s *Service) planOverview(req query.OverviewRequest) (plan, error) {
	var err error
	if req.From, req.To, err = s.resolve(req.From, req.To); err != nil {
		return plan{}, err
	}
	if err = req.Validate(); err != nil {
		return plan{}, err
	}
	if req.Interval == "" {
		req.Interval = query.IntervalHour
	}
	req.From, req.To = rollup.RoundOutwardHours(req.From, req.To)
	limit := MaxHourlyRange
	if req.Interval == query.IntervalDay {
		limit = MaxDailyRange
	}
	if req.To.Sub(req.From) > limit {
		return plan{}, fmt.Errorf("%w: range exceeds %s for interval %s", query.ErrInvalidRange, limit, req.Interval)
	}
	p := plan{req: req, rng: rollup.Range{FromNs: req.From.UnixNano(), ToNs: req.To.UnixNano()}}
	if req.Interval == query.IntervalHour {
		p.rollPlan = rollup.Plan{Hours: []rollup.Range{p.rng}}
		return p, nil
	}
	if p.rollPlan, err = rollup.PlanRange(p.rng); err != nil {
		return plan{}, fmt.Errorf("queries: plan range: %w", err)
	}
	return p, nil
}

func (p plan) bucket(ns int64) int64 {
	if p.req.Interval == query.IntervalDay {
		return rollup.DayStart(ns)
	}
	return ns
}

func readPlan[K rollup.Bucketed](
	p rollup.Plan, read func(rollup.Resolution, rollup.Range) ([]rollup.Entry[K], error),
) (days, hours []rollup.Entry[K], err error) {
	if !p.Days.IsEmpty() {
		if days, err = read(rollup.ResolutionDay, p.Days); err != nil {
			return nil, nil, fmt.Errorf("queries: read day rollups: %w", err)
		}
	}
	for _, h := range p.Hours {
		rows, err := read(rollup.ResolutionHour, h)
		if err != nil {
			return nil, nil, fmt.Errorf("queries: read hour rollups: %w", err)
		}
		hours = append(hours, rows...)
	}
	return days, hours, nil
}

func (s *Service) keyCells(ctx context.Context, p plan) (map[point]*rollup.Aggregate, error) {
	f := rollup.Filter{Kind: p.req.Kind}
	days, hours, err := readPlan(p.rollPlan, func(res rollup.Resolution, r rollup.Range) ([]rollup.Row, error) {
		return s.rollups.ReadRollups(ctx, res, r, f)
	})
	if err != nil {
		return nil, err
	}
	by := p.req.GroupBy
	return rollup.Assemble(p.rollPlan, days, hours, func(k rollup.Key) point {
		return point{bucket: p.bucket(k.BucketNs), key: groupKey(by, k)}
	}), nil
}

func groupKey(by query.GroupBy, k rollup.Key) string {
	switch by {
	case query.GroupModel:
		return k.Model
	case query.GroupProvider:
		return k.Provider
	case query.GroupService:
		return k.Service
	case query.GroupKind:
		return string(k.Kind)
	case query.GroupNone, query.GroupUser:
		return ""
	}
	return ""
}

func (s *Service) userCells(ctx context.Context, p plan) (map[point]*rollup.Aggregate, error) {
	days, hours, err := readPlan(p.rollPlan, func(res rollup.Resolution, r rollup.Range) ([]rollup.UserRow, error) {
		return s.rollups.ReadUserRollups(ctx, res, r)
	})
	if err != nil {
		return nil, err
	}
	return rollup.Assemble(p.rollPlan, days, hours, func(k rollup.UserKey) point {
		return point{bucket: p.bucket(k.BucketNs), key: k.UserID}
	}), nil
}

func buildOverview(p plan, cells map[point]*rollup.Aggregate) (Overview, error) {
	ov := Overview{From: p.req.From, To: p.req.To, GroupBy: p.req.GroupBy, Interval: p.req.Interval}
	totals := make(map[string]*rollup.Aggregate)
	for pt, a := range cells {
		mergeKey(totals, pt.key, a)
	}
	var all rollup.Aggregate
	for _, a := range totals {
		all.Merge(a)
	}
	var err error
	if ov.Total, err = newStats(&all); err != nil {
		return Overview{}, err
	}
	var rank map[string]int
	if p.req.GroupBy == query.GroupUser {
		ov.Groups, cells, rank, err = userGroups(totals, cells)
	} else {
		ov.Groups, rank, err = plainGroups(totals)
	}
	if err != nil {
		return Overview{}, err
	}
	ov.Series, err = buildSeries(cells, rank, p.req.GroupBy == query.GroupUser)
	if err != nil {
		return Overview{}, err
	}
	return ov, nil
}

func mergeKey(dst map[string]*rollup.Aggregate, k string, a *rollup.Aggregate) {
	acc, ok := dst[k]
	if !ok {
		acc = &rollup.Aggregate{}
		dst[k] = acc
	}
	acc.Merge(a)
}

func plainGroups(totals map[string]*rollup.Aggregate) ([]GroupStats, map[string]int, error) {
	keys := rankKeys(totals)
	groups := make([]GroupStats, 0, len(keys))
	rank := make(map[string]int, len(keys))
	for i, k := range keys {
		st, err := newStats(totals[k])
		if err != nil {
			return nil, nil, err
		}
		groups = append(groups, GroupStats{Key: k, Stats: st})
		rank[k] = i
	}
	return groups, rank, nil
}

// userGroups splits users into the top 50 and "other" and folds the series
// cells of the other users into one "other" cell per bucket. The empty "other"
// key cannot clash with a user because it is marked by the Other flag only.
func userGroups(
	totals map[string]*rollup.Aggregate, cells map[point]*rollup.Aggregate,
) ([]GroupStats, map[point]*rollup.Aggregate, map[string]int, error) {
	top := rollup.TopUsers(totals, rollup.DefaultTopUsers)
	groups := make([]GroupStats, 0, len(top.Top)+1)
	rank := make(map[string]int, len(top.Top))
	for i, u := range top.Top {
		st, err := newStats(&u.Aggregate)
		if err != nil {
			return nil, nil, nil, err
		}
		groups = append(groups, GroupStats{Key: userLabel(u.UserID), Stats: st})
		rank[u.UserID] = i
	}
	if top.OtherUsers == 0 {
		return groups, cells, rank, nil
	}
	st, err := newStats(&top.Other)
	if err != nil {
		return nil, nil, nil, err
	}
	groups = append(groups, GroupStats{Other: true, Users: top.OtherUsers, Stats: st})
	folded := make(map[point]*rollup.Aggregate, len(cells))
	for pt, a := range cells {
		if _, ok := rank[pt.key]; !ok {
			pt.key = otherKey
		}
		acc, ok := folded[pt]
		if !ok {
			acc = &rollup.Aggregate{}
			folded[pt] = acc
		}
		acc.Merge(a)
	}
	return groups, folded, rank, nil
}

// otherKey marks the folded cells of users outside the top list; user ids are
// never empty strings in rank, and the empty id is a real user ("(none)"), so
// a NUL-prefixed sentinel keeps the two apart.
const otherKey = "\x00other"

func userLabel(id string) string {
	if id == "" {
		return NoUser
	}
	return id
}

func rankKeys(totals map[string]*rollup.Aggregate) []string {
	keys := make([]string, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		x, y := totals[a].Measures, totals[b].Measures
		return cmp.Or(cmp.Compare(y.CostNanos, x.CostNanos), cmp.Compare(y.Count, x.Count), cmp.Compare(a, b))
	})
	return keys
}

func buildSeries(cells map[point]*rollup.Aggregate, rank map[string]int, users bool) ([]SeriesPoint, error) {
	pts := make([]point, 0, len(cells))
	for pt := range cells {
		pts = append(pts, pt)
	}
	order := func(k string) int {
		if r, ok := rank[k]; ok {
			return r
		}
		return len(rank) // other
	}
	slices.SortFunc(pts, func(a, b point) int {
		return cmp.Or(cmp.Compare(a.bucket, b.bucket), cmp.Compare(order(a.key), order(b.key)))
	})
	out := make([]SeriesPoint, 0, len(pts))
	for _, pt := range pts {
		st, err := newStats(cells[pt])
		if err != nil {
			return nil, err
		}
		sp := SeriesPoint{Bucket: time.Unix(0, pt.bucket).UTC(), Key: pt.key, Stats: st}
		if users {
			if _, ok := rank[pt.key]; ok {
				sp.Key = userLabel(pt.key)
			} else {
				sp.Key, sp.Other = "", true
			}
		}
		out = append(out, sp)
	}
	return out, nil
}
