// SPDX-License-Identifier: Apache-2.0

package query

import (
	"errors"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type GroupBy string

const (
	GroupNone     GroupBy = ""
	GroupModel    GroupBy = "model"
	GroupProvider GroupBy = "provider"
	GroupService  GroupBy = "service"
	GroupKind     GroupBy = "kind"
	GroupUser     GroupBy = "user"
)

type Interval string

const (
	IntervalHour Interval = "hour"
	IntervalDay  Interval = "day"
)

type TopBy string

const (
	TopByCost     TopBy = "cost"
	TopByDuration TopBy = "duration"
)

func ParseGroupBy(s string) (GroupBy, error) {
	switch g := GroupBy(s); g {
	case GroupNone, GroupModel, GroupProvider, GroupService, GroupKind, GroupUser:
		return g, nil
	default:
		return "", fmt.Errorf("%w: unknown group_by %q", ErrInvalidFilter, s)
	}
}

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

type OverviewRequest struct {
	From     time.Time
	To       time.Time
	GroupBy  GroupBy
	Interval Interval
	Kind     trace.Kind
}

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

type TopSpansRequest struct {
	From  time.Time
	To    time.Time
	By    TopBy
	Limit int
}

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
