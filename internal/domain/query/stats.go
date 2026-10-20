// SPDX-License-Identifier: Apache-2.0

package query

import (
	"errors"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// GroupBy is the overview grouping dimension.
type GroupBy string

// Overview groupings.
const (
	GroupNone     GroupBy = ""
	GroupModel    GroupBy = "model"
	GroupProvider GroupBy = "provider"
	GroupService  GroupBy = "service"
	GroupKind     GroupBy = "kind"
	GroupUser     GroupBy = "user"
)

// Interval is the overview time bucket.
type Interval string

// Overview intervals.
const (
	IntervalHour Interval = "hour"
	IntervalDay  Interval = "day"
)

// TopBy is the ranking of top spans.
type TopBy string

// Top-span rankings.
const (
	TopByCost     TopBy = "cost"
	TopByDuration TopBy = "duration"
)

// ParseGroupBy parses a group_by value; empty means no grouping.
func ParseGroupBy(s string) (GroupBy, error) {
	switch g := GroupBy(s); g {
	case GroupNone, GroupModel, GroupProvider, GroupService, GroupKind, GroupUser:
		return g, nil
	default:
		return "", fmt.Errorf("%w: unknown group_by %q", ErrInvalidFilter, s)
	}
}

// ParseInterval parses an interval; empty means IntervalHour.
func ParseInterval(s string) (Interval, error) {
	switch i := Interval(s); i {
	case "":
		return IntervalHour, nil
	case IntervalHour, IntervalDay:
		return i, nil
	default:
		return "", fmt.Errorf("%w: unknown interval %q", ErrInvalidFilter, s)
	}
}

// ParseTopBy parses a by value; empty means TopByCost.
func ParseTopBy(s string) (TopBy, error) {
	switch b := TopBy(s); b {
	case "":
		return TopByCost, nil
	case TopByCost, TopByDuration:
		return b, nil
	default:
		return "", fmt.Errorf("%w: unknown by %q", ErrInvalidFilter, s)
	}
}

// OverviewRequest asks for aggregate statistics over [From, To). An empty Kind
// is valid here; the API layer decides whether that means llm or all kinds.
type OverviewRequest struct {
	From     time.Time
	To       time.Time
	GroupBy  GroupBy
	Interval Interval
	Kind     trace.Kind
}

// Validate returns every problem found in the request, joined; nil when valid.
func (r OverviewRequest) Validate() error {
	var errs []error
	if !r.From.Before(r.To) {
		errs = append(errs, fmt.Errorf("%w: from is not before to", ErrInvalidRange))
	}
	if _, err := ParseGroupBy(string(r.GroupBy)); err != nil {
		errs = append(errs, err)
	}
	if _, err := ParseInterval(string(r.Interval)); err != nil {
		errs = append(errs, err)
	}
	if r.Kind != "" && !r.Kind.IsValid() {
		errs = append(errs, fmt.Errorf("%w: unknown kind %q", ErrInvalidFilter, r.Kind))
	}
	return errors.Join(errs...)
}

// TopSpansRequest asks for the most expensive or slowest llm spans in [From, To).
type TopSpansRequest struct {
	From  time.Time
	To    time.Time
	By    TopBy
	Limit int
}

// Validate returns every problem found in the request, joined; nil when valid.
func (r TopSpansRequest) Validate() error {
	var errs []error
	if !r.From.Before(r.To) {
		errs = append(errs, fmt.Errorf("%w: from is not before to", ErrInvalidRange))
	}
	if _, err := ParseTopBy(string(r.By)); err != nil {
		errs = append(errs, err)
	}
	if _, err := NormalizeLimit(r.Limit); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
