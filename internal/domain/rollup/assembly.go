// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"fmt"
	"time"
)

type Range struct {
	FromNs int64
	ToNs   int64
}

func (r Range) IsEmpty() bool { return r.FromNs >= r.ToNs }

func (r Range) Contains(ns int64) bool { return ns >= r.FromNs && ns < r.ToNs }

type Plan struct {
	Days  Range
	Hours []Range
}

func RoundOutwardHours(from, to time.Time) (time.Time, time.Time) {
	from, to = from.UTC().Truncate(time.Hour), to.UTC()
	if r := to.Truncate(time.Hour); r.Before(to) {
		to = r.Add(time.Hour)
	}
	return from, to
}

// PlanRange splits a range into whole UTC days and edge hours.
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

func (p Plan) HasHour(ns int64) bool {
	for _, h := range p.Hours {
		if h.Contains(ns) {
			return true
		}
	}
	return false
}

type Bucketed interface {
	comparable
	Bucket() int64
}

type Entry[K Bucketed] struct {
	Key       K
	Aggregate Aggregate
}

type Row = Entry[Key]

type UserRow = Entry[UserKey]

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

// Assemble merges day and edge-hour rows into one aggregate per group.
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
