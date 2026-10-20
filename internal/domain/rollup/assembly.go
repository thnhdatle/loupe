// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"fmt"
	"time"
)

// Range is the half-open interval [FromNs, ToNs) of Unix nanoseconds.
type Range struct {
	FromNs int64
	ToNs   int64
}

// IsEmpty reports whether the range holds no instant.
func (r Range) IsEmpty() bool { return r.FromNs >= r.ToNs }

// Contains reports whether ns lies in the range.
func (r Range) Contains(ns int64) bool { return ns >= r.FromNs && ns < r.ToNs }

// Plan says which rollup rows make up a range: the day rows whose day start is
// in Days and the hour rows whose hour start is in one of Hours.
type Plan struct {
	Days  Range   // whole UTC days; empty when the range holds none
	Hours []Range // the edge hours before and after Days, 0 to 2 ranges
}

// RoundOutwardHours widens [from, to) to whole UTC hours: from rounds down, to rounds up.
func RoundOutwardHours(from, to time.Time) (time.Time, time.Time) {
	from, to = from.UTC().Truncate(time.Hour), to.UTC()
	if r := to.Truncate(time.Hour); r.Before(to) {
		to = r.Add(time.Hour)
	}
	return from, to
}

// PlanRange splits r, whose bounds are whole hours, into whole UTC days and
// edge hours. It returns ErrInvalidRange for an empty or unaligned range.
func PlanRange(r Range) (Plan, error) {
	if r.IsEmpty() || HourStart(r.FromNs) != r.FromNs || HourStart(r.ToNs) != r.ToNs {
		return Plan{}, fmt.Errorf("%w: [%d, %d) is empty or not whole hours", ErrInvalidRange, r.FromNs, r.ToNs)
	}
	firstDay := DayStart(r.FromNs)
	if firstDay < r.FromNs {
		firstDay += DayNs
	}
	endDay := DayStart(r.ToNs)
	if firstDay >= endDay {
		return Plan{Hours: []Range{r}}, nil
	}
	p := Plan{Days: Range{FromNs: firstDay, ToNs: endDay}}
	if r.FromNs < firstDay {
		p.Hours = append(p.Hours, Range{FromNs: r.FromNs, ToNs: firstDay})
	}
	if endDay < r.ToNs {
		p.Hours = append(p.Hours, Range{FromNs: endDay, ToNs: r.ToNs})
	}
	return p, nil
}

// HasHour reports whether an hour row starting at ns belongs to the plan.
func (p Plan) HasHour(ns int64) bool {
	for _, h := range p.Hours {
		if h.Contains(ns) {
			return true
		}
	}
	return false
}

// Bucketed is a rollup key with a bucket start.
type Bucketed interface {
	comparable
	Bucket() int64
}

// Entry is one rollup row.
type Entry[K Bucketed] struct {
	Key       K
	Aggregate Aggregate
}

// Row is a row of rollup_hour or rollup_day.
type Row = Entry[Key]

// UserRow is a row of rollup_user_hour or rollup_user_day.
type UserRow = Entry[UserKey]

// MergeBy adds the aggregate of every row to dst[group(row.Key)], allocating
// missing entries. dst never shares storage with rows.
func MergeBy[K Bucketed, G comparable](dst map[G]*Aggregate, rows []Entry[K], group func(K) G) {
	for i := range rows {
		mergeInto(dst, group(rows[i].Key), &rows[i].Aggregate)
	}
}

func mergeInto[G comparable](dst map[G]*Aggregate, g G, a *Aggregate) {
	acc, ok := dst[g]
	if !ok {
		acc = &Aggregate{}
		dst[g] = acc
	}
	acc.Merge(a)
}

// Assemble merges, grouped by group, the day rows whose bucket is in p.Days and
// the hour rows whose bucket is in p.Hours; other rows are ignored, so readers
// may return a superset.
func Assemble[K Bucketed, G comparable](p Plan, days, hours []Entry[K], group func(K) G) map[G]*Aggregate {
	dst := make(map[G]*Aggregate)
	for i := range days {
		if p.Days.Contains(days[i].Key.Bucket()) {
			mergeInto(dst, group(days[i].Key), &days[i].Aggregate)
		}
	}
	for i := range hours {
		if p.HasHour(hours[i].Key.Bucket()) {
			mergeInto(dst, group(hours[i].Key), &hours[i].Aggregate)
		}
	}
	return dst
}
