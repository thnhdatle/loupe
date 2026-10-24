// SPDX-License-Identifier: Apache-2.0

package queries

import (
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const NoUser = "(none)"

const (
	MaxHourlyRange = 31 * 24 * time.Hour
	MaxDailyRange  = 366 * 24 * time.Hour
)

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

type GroupStats struct {
	Key   string
	Other bool
	Users int
	Stats Stats
}

type SeriesPoint struct {
	Bucket time.Time
	Key    string
	Other  bool
	Stats  Stats
}

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
