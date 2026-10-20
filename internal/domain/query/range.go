// SPDX-License-Identifier: Apache-2.0

package query

import (
	"fmt"
	"time"
)

// DefaultRange is the span of the default time range.
const DefaultRange = 24 * time.Hour

// ResolveRange applies the default range (the last 24 hours ending at now) to
// missing bounds and checks from < to. Results are in UTC. A missing from with
// an explicit to defaults to to minus 24 hours.
func ResolveRange(from, to *time.Time, now time.Time) (time.Time, time.Time, error) {
	end := now.UTC()
	if to != nil {
		end = to.UTC()
	}
	start := end.Add(-DefaultRange)
	if from != nil {
		start = from.UTC()
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: from %s is not before to %s",
			ErrInvalidRange, start.Format(time.RFC3339), end.Format(time.RFC3339))
	}
	return start, end, nil
}

// RoundOutwardHours widens [from, to) to whole UTC hours: from rounds down, to rounds up.
func RoundOutwardHours(from, to time.Time) (time.Time, time.Time) {
	from, to = from.UTC().Truncate(time.Hour), to.UTC()
	if r := to.Truncate(time.Hour); r.Before(to) {
		to = r.Add(time.Hour)
	}
	return from, to
}
