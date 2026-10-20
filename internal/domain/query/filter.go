// SPDX-License-Identifier: Apache-2.0

package query

import (
	"errors"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// SpanFilter holds span-level conditions. Every set field must match the same span.
type SpanFilter struct {
	Service       string
	Model         string
	Provider      string
	Kind          trace.Kind
	Status        trace.Status
	MinDurationNs int64
	// NamePrefix is a case-sensitive prefix of the span name.
	NamePrefix string
}

// IsEmpty reports whether no span-level condition is set.
func (f SpanFilter) IsEmpty() bool { return f == SpanFilter{} }

// TraceFilter selects traces. The range [From, To) applies to the trace start;
// a zero bound is open. Pointer fields are unset when nil.
type TraceFilter struct {
	From         time.Time
	To           time.Time
	SessionID    string
	UserID       string
	MinCostNanos *int64
	HasError     *bool
	Span         SpanFilter
}

// Validate returns every problem found in the filter, joined; nil when valid.
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

// SessionFilter selects sessions by last activity in [From, To) and user.
type SessionFilter struct {
	From   time.Time
	To     time.Time
	UserID string
}
