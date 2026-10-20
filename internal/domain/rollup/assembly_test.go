// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var day0 = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).UnixNano()

func hourAt(h int64) int64 { return day0 + h*rollup.HourNs }

func TestRoundOutwardHours(t *testing.T) {
	t.Parallel()
	at := func(h, m int) time.Time { return time.Date(2026, 10, 19, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name             string
		from, to         time.Time
		wantFrom, wantTo time.Time
	}{
		{name: "aligned", from: at(1, 0), to: at(3, 0), wantFrom: at(1, 0), wantTo: at(3, 0)},
		{name: "inside", from: at(1, 10), to: at(3, 50), wantFrom: at(1, 0), wantTo: at(4, 0)},
		{
			name: "non-UTC input", from: at(1, 10).In(time.FixedZone("x", 5400)), to: at(2, 0),
			wantFrom: at(1, 0), wantTo: at(2, 0),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			from, to := rollup.RoundOutwardHours(tc.from, tc.to)
			assert.Equal(t, tc.wantFrom, from)
			assert.Equal(t, tc.wantTo, to)
			assert.Equal(t, time.UTC, from.Location())
		})
	}
}

func TestPlanRange(t *testing.T) {
	t.Parallel()
	r := func(from, to int64) rollup.Range { return rollup.Range{FromNs: hourAt(from), ToNs: hourAt(to)} }
	cases := []struct {
		name string
		in   rollup.Range
		want rollup.Plan
	}{
		{name: "within one day", in: r(2, 5), want: rollup.Plan{Hours: []rollup.Range{r(2, 5)}}},
		{name: "across midnight, no whole day", in: r(20, 30), want: rollup.Plan{Hours: []rollup.Range{r(20, 30)}}},
		{name: "exactly one day", in: r(0, 24), want: rollup.Plan{Days: r(0, 24)}},
		{name: "day plus trailing hours", in: r(0, 27), want: rollup.Plan{Days: r(0, 24), Hours: []rollup.Range{r(24, 27)}}},
		{name: "leading hours plus day", in: r(-3, 24), want: rollup.Plan{Days: r(0, 24), Hours: []rollup.Range{r(-3, 0)}}},
		{name: "both edges", in: r(5, 72+1), want: rollup.Plan{
			Days: r(24, 72), Hours: []rollup.Range{r(5, 24), r(72, 73)},
		}},
		{name: "single hour", in: r(23, 24), want: rollup.Plan{Hours: []rollup.Range{r(23, 24)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := rollup.PlanRange(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPlanRange_Invalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   rollup.Range
	}{
		{name: "empty", in: rollup.Range{FromNs: hourAt(1), ToNs: hourAt(1)}},
		{name: "reversed", in: rollup.Range{FromNs: hourAt(2), ToNs: hourAt(1)}},
		{name: "unaligned from", in: rollup.Range{FromNs: hourAt(1) + 1, ToNs: hourAt(3)}},
		{name: "unaligned to", in: rollup.Range{FromNs: hourAt(1), ToNs: hourAt(3) - 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := rollup.PlanRange(tc.in)
			require.ErrorIs(t, err, rollup.ErrInvalidRange)
		})
	}
}

func TestRange_ContainsAndPlanHasHour(t *testing.T) {
	t.Parallel()
	r := rollup.Range{FromNs: 10, ToNs: 20}
	assert.True(t, r.Contains(10))
	assert.True(t, r.Contains(19))
	assert.False(t, r.Contains(20))
	assert.False(t, r.Contains(9))
	assert.False(t, r.IsEmpty())
	assert.True(t, rollup.Range{FromNs: 5, ToNs: 5}.IsEmpty())
	p := rollup.Plan{Hours: []rollup.Range{{FromNs: 0, ToNs: 2}, {FromNs: 8, ToNs: 9}}}
	assert.True(t, p.HasHour(1))
	assert.True(t, p.HasHour(8))
	assert.False(t, p.HasHour(5))
}

// store simulates the writer: every span updates the row of its own hour and of its own day.
type store struct {
	hours, days         map[rollup.Key]*rollup.Aggregate
	userHours, userDays map[rollup.UserKey]*rollup.Aggregate
}

func newStore() *store {
	return &store{
		hours: map[rollup.Key]*rollup.Aggregate{}, days: map[rollup.Key]*rollup.Aggregate{},
		userHours: map[rollup.UserKey]*rollup.Aggregate{}, userDays: map[rollup.UserKey]*rollup.Aggregate{},
	}
}

func row[K comparable](m map[K]*rollup.Aggregate, k K) *rollup.Aggregate {
	a, ok := m[k]
	if !ok {
		a = &rollup.Aggregate{}
		m[k] = a
	}
	return a
}

func (s *store) write(sp *trace.Span) {
	row(s.hours, rollup.KeyOf(sp, rollup.ResolutionHour)).AddSpan(sp)
	row(s.days, rollup.KeyOf(sp, rollup.ResolutionDay)).AddSpan(sp)
	row(s.userHours, rollup.UserKeyOf(sp.UserID, sp.StartNs, rollup.ResolutionHour)).AddSpan(sp)
	row(s.userDays, rollup.UserKeyOf(sp.UserID, sp.StartNs, rollup.ResolutionDay)).AddSpan(sp)
}

func entries[K rollup.Bucketed](m map[K]*rollup.Aggregate) []rollup.Entry[K] {
	out := make([]rollup.Entry[K], 0, len(m))
	for k, a := range m {
		out = append(out, rollup.Entry[K]{Key: k, Aggregate: *a})
	}
	return out
}

func inRange[K rollup.Bucketed](rows []rollup.Entry[K], r rollup.Range) []rollup.Entry[K] {
	return slices.DeleteFunc(slices.Clone(rows), func(e rollup.Entry[K]) bool { return !r.Contains(e.Key.Bucket()) })
}

func spanGen() *rapid.Generator[trace.Span] {
	return rapid.Custom(func(t *rapid.T) trace.Span {
		start := day0 + rapid.Int64Range(-2*rollup.DayNs, 3*rollup.DayNs).Draw(t, "start")
		s := trace.Span{
			StartNs:  start,
			EndNs:    start + rapid.Int64Range(0, int64(time.Minute)).Draw(t, "dur"),
			Service:  rapid.SampledFrom([]string{"api", "worker"}).Draw(t, "service"),
			Model:    rapid.SampledFrom([]string{"gpt", "claude", ""}).Draw(t, "model"),
			Kind:     rapid.SampledFrom([]trace.Kind{trace.KindLLM, trace.KindTool}).Draw(t, "kind"),
			UserID:   rapid.SampledFrom([]string{"", "u1", "u2"}).Draw(t, "user"),
			Usage:    trace.Usage{Input: rapid.Int64Range(0, 100).Draw(t, "in")},
			HasUsage: rapid.Bool().Draw(t, "has_usage"),
		}
		if rapid.Bool().Draw(t, "priced") {
			s.Cost = trace.Priced(rapid.Int64Range(0, 1000).Draw(t, "cost"))
		}
		if rapid.Bool().Draw(t, "error") {
			s.Status = trace.StatusError
		}
		if rapid.IntRange(0, 4).Draw(t, "dup") == 0 {
			s.Flags = trace.FlagUsageDuplicate
		}
		return s
	})
}

func assertSameTotals[G comparable](t *rapid.T, want, got map[G]*rollup.Aggregate) {
	require.ElementsMatch(t, slices.Collect(maps.Keys(want)), slices.Collect(maps.Keys(got)))
	for g, w := range want {
		require.Equal(t, w.Measures, got[g].Measures, "group %v", g)
		require.Equal(t, blob(t, &w.Histogram), blob(t, &got[g].Histogram), "group %v", g)
	}
}

// TestAssemble_DaysAndEdgeHoursEqualHourlyMergeProperty checks that
// hour + day assembly equals a pure merge of all hourly rows, also for user rollups.
func TestAssemble_DaysAndEdgeHoursEqualHourlyMergeProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		st := newStore()
		for _, sp := range rapid.SliceOfN(spanGen(), 0, 150).Draw(t, "spans") {
			st.write(&sp)
		}
		from := rapid.Int64Range(-3*24, 4*24).Draw(t, "from_hour")
		r := rollup.Range{FromNs: hourAt(from), ToNs: hourAt(from + rapid.Int64Range(1, 5*24).Draw(t, "hours"))}
		p, err := rollup.PlanRange(r)
		require.NoError(t, err)

		type group struct {
			service, model string
			kind           trace.Kind
		}
		byGroup := func(k rollup.Key) group { return group{k.Service, k.Model, k.Kind} }
		hours, days := entries(st.hours), entries(st.days)
		want := map[group]*rollup.Aggregate{}
		rollup.MergeBy(want, inRange(hours, r), byGroup)
		assertSameTotals(t, want, rollup.Assemble(p, days, hours, byGroup))

		byUser := func(k rollup.UserKey) string { return k.UserID }
		userHours, userDays := entries(st.userHours), entries(st.userDays)
		wantUsers := map[string]*rollup.Aggregate{}
		rollup.MergeBy(wantUsers, inRange(userHours, r), byUser)
		assertSameTotals(t, wantUsers, rollup.Assemble(p, userDays, userHours, byUser))
	})
}
