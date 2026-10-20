// SPDX-License-Identifier: Apache-2.0

package queries

import (
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// NoUser is the key shown for spans attributed to no user.
const NoUser = "(none)"

// Range limits of Overview, measured on the range rounded outward to whole hours.
const (
	MaxHourlyRange = 31 * 24 * time.Hour
	MaxDailyRange  = 366 * 24 * time.Hour
)

// Stats are the aggregate measures of a set of spans. Requests, Errors, tokens
// and cost exclude spans flagged usage_duplicate; latency percentiles (in
// nanoseconds) cover every span and are zero when there are none.
type Stats struct {
	Requests  int64
	Errors    int64
	Usage     trace.Usage
	CostNanos int64
	Unpriced  int64
	P50Ns     int64
	P95Ns     int64
	P99Ns     int64
}

// GroupStats is the total of one group over the range. For group_by=user,
// Other marks the merge of every user outside the top 50 (Key is empty) and
// Users counts the users merged into it.
type GroupStats struct {
	Key   string
	Other bool
	Users int
	Stats Stats
}

// SeriesPoint is the total of one group in one time bucket (UTC, start of the
// hour or day depending on the interval). Empty buckets are omitted.
type SeriesPoint struct {
	Bucket time.Time
	Key    string
	Other  bool
	Stats  Stats
}

// Overview is the result of Service.Overview. From and To are the effective
// range, rounded outward to whole hours. Groups are ranked by cost descending;
// Series is ordered by bucket, then group rank.
type Overview struct {
	From     time.Time
	To       time.Time
	GroupBy  query.GroupBy
	Interval query.Interval
	Total    Stats
	Groups   []GroupStats
	Series   []SeriesPoint
}

func newStats(a *rollup.Aggregate) (Stats, error) {
	m := a.Measures
	st := Stats{Requests: m.Count, Errors: m.Errors, Usage: m.Usage, CostNanos: m.CostNanos, Unpriced: m.Unpriced}
	if a.Histogram.IsEmpty() {
		return st, nil
	}
	for _, q := range []struct {
		q   float64
		dst *int64
	}{{0.50, &st.P50Ns}, {0.95, &st.P95Ns}, {0.99, &st.P99Ns}} {
		v, err := a.Histogram.Quantile(q.q)
		if err != nil {
			return Stats{}, fmt.Errorf("queries: quantile %v: %w", q.q, err)
		}
		*q.dst = v
	}
	return st, nil
}
