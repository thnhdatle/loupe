// SPDX-License-Identifier: Apache-2.0

package queries_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func agg(count, errs, cost int64, durs ...int64) rollup.Aggregate {
	a := rollup.Aggregate{Measures: rollup.Measures{
		Count: count, Errors: errs, CostNanos: cost, Unpriced: 1, Usage: trace.Usage{Input: count * 10, Output: count},
	}}
	for _, d := range durs {
		a.Histogram.Add(d)
	}
	return a
}

func hourRow(t time.Time, model string, kind trace.Kind, a rollup.Aggregate) rollup.Row {
	k := rollup.Key{BucketNs: t.UnixNano(), Service: "svc", Provider: "p-" + model, Model: model, Kind: kind}
	return rollup.Row{Key: k, Aggregate: a}
}

var (
	day1 = time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	day2 = day1.Add(24 * time.Hour)
)

func fixtureRollups() *fakeRollups {
	f := &fakeRollups{}
	h := time.Hour
	f.hour = []rollup.Row{
		hourRow(day1.Add(-2*h), "m1", trace.KindLLM, agg(1, 0, 100, 1e6)),
		hourRow(day1.Add(3*h), "m1", trace.KindLLM, agg(2, 1, 200, 2e6, 3e6)),
		hourRow(day2.Add(5*h), "m2", trace.KindLLM, agg(3, 0, 300, 4e6)),
		hourRow(day2.Add(5*h), "t", trace.KindTool, agg(9, 0, 0, 1e5)),
		hourRow(day2.Add(25*h), "m1", trace.KindLLM, agg(4, 2, 400, 5e6)),
	}
	f.day = []rollup.Row{
		hourRow(day1, "m1", trace.KindLLM, agg(2, 1, 200, 2e6, 3e6)),
		hourRow(day2, "m2", trace.KindLLM, agg(3, 0, 300, 4e6)),
		hourRow(day2, "t", trace.KindTool, agg(9, 0, 0, 1e5)),
	}
	return f
}

func TestService_Overview_DayAssemblyMatchesPureHourMerge(t *testing.T) {
	t.Parallel()
	from, to := day1.Add(-2*time.Hour+10*time.Minute), day2.Add(26*time.Hour+time.Second)
	ro := fixtureRollups()
	svc := newSvc(&fakeTraces{}, &fakeSessions{}, ro)
	day, err := svc.Overview(context.Background(), query.OverviewRequest{
		From: from, To: to, GroupBy: query.GroupModel, Interval: query.IntervalDay, Kind: trace.KindLLM,
	})
	require.NoError(t, err)
	hour, err := svc.Overview(context.Background(), query.OverviewRequest{
		From: from, To: to, GroupBy: query.GroupModel, Interval: query.IntervalHour, Kind: trace.KindLLM,
	})
	require.NoError(t, err)

	assert.Equal(t, day1.Add(-2*time.Hour), day.From)
	assert.Equal(t, day2.Add(27*time.Hour), day.To)
	assert.Equal(t, day.Total, hour.Total)
	assert.Equal(t, day.Groups, hour.Groups)
	assert.Equal(t, queries.Stats{
		Requests: 10, Errors: 3, CostNanos: 1000, Unpriced: 4,
		Usage: trace.Usage{Input: 100, Output: 10}, P50Ns: day.Total.P50Ns, P95Ns: day.Total.P95Ns, P99Ns: day.Total.P99Ns,
	}, day.Total)
	assert.Positive(t, day.Total.P95Ns)
	require.Len(t, day.Groups, 2)
	assert.Equal(t, "m1", day.Groups[0].Key)
	assert.Equal(t, int64(700), day.Groups[0].Stats.CostNanos)
	assert.Equal(t, "m2", day.Groups[1].Key)

	assert.Len(t, hour.Series, 4)
	require.Len(t, day.Series, 4)
	assert.Equal(t, day1.Add(-24*time.Hour), day.Series[0].Bucket)
	assert.Equal(t, "m1", day.Series[0].Key)
	assert.Equal(t, day1, day.Series[1].Bucket)

	var dayReads, hourReads int
	for _, c := range ro.calls[:3] {
		if c.Res == rollup.ResolutionDay {
			dayReads++
			assert.Equal(t, day1.UnixNano(), c.R.FromNs)
			assert.Equal(t, day2.Add(24*time.Hour).UnixNano(), c.R.ToNs)
		} else {
			hourReads++
		}
	}
	assert.Equal(t, 1, dayReads)
	assert.Equal(t, 2, hourReads)
}

func TestService_Overview_Groupings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		by   query.GroupBy
		kind trace.Kind
		want []string
	}{
		{name: "none", by: query.GroupNone, kind: trace.KindLLM, want: []string{""}},
		{name: "model", by: query.GroupModel, kind: trace.KindLLM, want: []string{"m2", "m1"}},
		{name: "provider", by: query.GroupProvider, kind: trace.KindLLM, want: []string{"p-m2", "p-m1"}},
		{name: "service", by: query.GroupService, kind: trace.KindLLM, want: []string{"svc"}},
		{name: "kind all", by: query.GroupKind, want: []string{"llm", "tool"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newSvc(&fakeTraces{}, &fakeSessions{}, fixtureRollups())
			ov, err := svc.Overview(context.Background(), query.OverviewRequest{
				From: day1.Add(-24 * time.Hour), To: day2.Add(48 * time.Hour),
				GroupBy: tc.by, Interval: query.IntervalDay, Kind: tc.kind,
			})
			require.NoError(t, err)
			var keys []string
			for _, g := range ov.Groups {
				keys = append(keys, g.Key)
			}
			assert.Equal(t, tc.want, keys)
			assert.Equal(t, tc.by, ov.GroupBy)
		})
	}
}

func userRows(n int, withNone bool) *fakeRollups {
	f := &fakeRollups{}
	for i := range n {
		id := fmt.Sprintf("u%02d", i)
		a := agg(1, 0, int64(1000-i), 1e6)
		f.uDay = append(f.uDay, rollup.UserRow{Key: rollup.UserKey{BucketNs: day1.UnixNano(), UserID: id}, Aggregate: a})
		b := agg(1, 0, int64(1000-i), 1e6)
		f.uDay = append(f.uDay, rollup.UserRow{Key: rollup.UserKey{BucketNs: day2.UnixNano(), UserID: id}, Aggregate: b})
	}
	if withNone {
		k := rollup.UserKey{BucketNs: day1.Add(-time.Hour).UnixNano()}
		f.uHour = append(f.uHour, rollup.UserRow{Key: k, Aggregate: agg(1, 0, 1, 1e6)})
	}
	return f
}

func TestService_Overview_Users(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		users      int
		wantGroups int
		wantOther  int
	}{
		{name: "few users no other", users: 3, wantGroups: 4},
		{name: "top 50 plus other", users: 60, wantGroups: 51, wantOther: 11},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ro := userRows(tc.users, true)
			ov, err := newSvc(&fakeTraces{}, &fakeSessions{}, ro).Overview(context.Background(), query.OverviewRequest{
				From: day1.Add(-time.Hour), To: day2.Add(24 * time.Hour), GroupBy: query.GroupUser, Interval: query.IntervalDay,
				Kind: trace.KindLLM,
			})
			require.NoError(t, err)
			require.Len(t, ov.Groups, tc.wantGroups)
			assert.Equal(t, "u00", ov.Groups[0].Key)
			assert.Equal(t, int64(2000), ov.Groups[0].Stats.CostNanos)
			assert.Equal(t, int64(2*tc.users+1), ov.Total.Requests)
			last := ov.Groups[len(ov.Groups)-1]
			if tc.wantOther > 0 {
				assert.True(t, last.Other)
				assert.Equal(t, tc.wantOther, last.Users)
				var sum int64
				for _, p := range ov.Series {
					if p.Other {
						sum += p.Stats.Requests
					}
				}
				assert.Equal(t, last.Stats.Requests, sum)
			} else {
				assert.Equal(t, queries.NoUser, last.Key)
				assert.False(t, last.Other)
			}
			var seriesReq int64
			for _, p := range ov.Series {
				seriesReq += p.Stats.Requests
			}
			assert.Equal(t, ov.Total.Requests, seriesReq)
		})
	}
}

func TestService_Overview_Errors(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	tests := []struct {
		name    string
		req     query.OverviewRequest
		ro      *fakeRollups
		wantErr error
	}{
		{
			name:    "from after to",
			req:     query.OverviewRequest{From: now, To: now.Add(-time.Hour)},
			ro:      &fakeRollups{},
			wantErr: query.ErrInvalidRange,
		},
		{
			name:    "bad group",
			req:     query.OverviewRequest{GroupBy: "x"},
			ro:      &fakeRollups{},
			wantErr: query.ErrInvalidFilter,
		},
		{
			name:    "bad interval",
			req:     query.OverviewRequest{Interval: "week"},
			ro:      &fakeRollups{},
			wantErr: query.ErrInvalidFilter,
		},
		{name: "bad kind", req: query.OverviewRequest{Kind: "x"}, ro: &fakeRollups{}, wantErr: query.ErrInvalidFilter},
		{
			name:    "hour range too long",
			req:     query.OverviewRequest{From: now.Add(-32 * 24 * time.Hour)},
			ro:      &fakeRollups{},
			wantErr: query.ErrInvalidRange,
		},
		{
			name:    "day range too long",
			req:     query.OverviewRequest{From: now.Add(-400 * 24 * time.Hour), Interval: query.IntervalDay},
			ro:      &fakeRollups{},
			wantErr: query.ErrInvalidRange,
		},
		{name: "hour read fails", req: query.OverviewRequest{}, ro: &fakeRollups{err: boom}, wantErr: boom},
		{
			name:    "day read fails",
			req:     query.OverviewRequest{From: now.Add(-72 * time.Hour), Interval: query.IntervalDay},
			ro:      &fakeRollups{err: boom, failOn: rollup.ResolutionDay},
			wantErr: boom,
		},
		{
			name:    "edge hour read fails",
			req:     query.OverviewRequest{From: now.Add(-72 * time.Hour), Interval: query.IntervalDay},
			ro:      &fakeRollups{err: boom, failOn: rollup.ResolutionHour},
			wantErr: boom,
		},
		{
			name:    "user read fails",
			req:     query.OverviewRequest{GroupBy: query.GroupUser},
			ro:      &fakeRollups{err: boom},
			wantErr: boom,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newSvc(&fakeTraces{}, &fakeSessions{}, tc.ro).Overview(context.Background(), tc.req)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestService_Overview_EmptyAndDefaults(t *testing.T) {
	t.Parallel()
	svc := newSvc(&fakeTraces{}, &fakeSessions{}, &fakeRollups{})
	ov, err := svc.Overview(context.Background(), query.OverviewRequest{})
	require.NoError(t, err)
	assert.Equal(t, queries.Stats{}, ov.Total)
	assert.Empty(t, ov.Groups)
	assert.Empty(t, ov.Series)
	assert.Equal(t, query.IntervalHour, ov.Interval)
	assert.Equal(t, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), ov.From)
	assert.Equal(t, time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC), ov.To)
}

func TestService_ModelStatsAndUserStats(t *testing.T) {
	t.Parallel()
	ro := fixtureRollups()
	ro.uDay = userRows(2, false).uDay
	svc := newSvc(&fakeTraces{}, &fakeSessions{}, ro)
	from, to := day1.Add(-24*time.Hour), day2.Add(48*time.Hour)

	models, err := svc.ModelStats(context.Background(), from, to)
	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "m2", models[0].Key)

	users, err := svc.UserStats(context.Background(), from, to)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "u00", users[0].Key)

	_, err = svc.ModelStats(context.Background(), to, from)
	require.ErrorIs(t, err, query.ErrInvalidRange)
	_, err = svc.UserStats(context.Background(), to, from)
	require.ErrorIs(t, err, query.ErrInvalidRange)
}
