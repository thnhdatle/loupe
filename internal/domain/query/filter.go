// SPDX-License-Identifier: Apache-2.0

package query

import (
	"errors"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type SpanFilter struct {
	Service       string
	Model         string
	Provider      string
	Kind          trace.Kind
	Status        trace.Status
	MinDurationNs int64
	NamePrefix    string
}

func (f SpanFilter) IsEmpty() bool { return f == SpanFilter{} }

type TraceFilter struct {
	From         time.Time
	To           time.Time
	SessionID    string
	UserID       string
	MinCostNanos *int64
	HasError     *bool
	Span         SpanFilter
}

func (f TraceFilter) Validate() error {
	var errs []error
	if !f.From.IsZero() && !f.To.IsZero() && !f.From.Before(f.To) {
		errs = append(errs, fmt.Errorf("%w: from is not before to", ErrInvalidRange))
	}
	if f.MinCostNanos != nil && *f.MinCostNanos < 0 {
		errs = append(errs, fmt.Errorf("%w: negative min cost", ErrInvalidFilter))
	}
	if f.Span.MinDurationNs < 0 {
		errs = append(errs, fmt.Errorf("%w: negative min duration", ErrInvalidFilter))
	}
	if f.Span.Kind != "" && !f.Span.Kind.IsValid() {
		errs = append(errs, fmt.Errorf("%w: unknown kind %q", ErrInvalidFilter, f.Span.Kind))
	}
	switch f.Span.Status {
	case "", trace.StatusUnset, trace.StatusOK, trace.StatusError:
	default:
		errs = append(errs, fmt.Errorf("%w: unknown status %q", ErrInvalidFilter, f.Span.Status))
	}
	return errors.Join(errs...)
}

type SessionFilter struct {
	From   time.Time
	To     time.Time
	UserID string
}
