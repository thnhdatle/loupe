// SPDX-License-Identifier: Apache-2.0

package rollup

import "github.com/thnhdatle/loupe/internal/domain/trace"

// Measures are the additive parts of a rollup row. They exclude spans flagged
// usage_duplicate; the writer moves them with Add and Sub when flags change, so
// any field may be negative in a delta.
type Measures struct {
	Count     int64 // spans
	Errors    int64 // spans with status error
	Usage     trace.Usage
	CostNanos int64 // sum of priced costs
	Unpriced  int64 // spans with usage but no price
}

// SpanMeasures returns the contribution of s ignoring its usage_duplicate flag.
func SpanMeasures(s *trace.Span) Measures {
	m := Measures{Count: 1, Usage: s.Usage}
	if s.Status == trace.StatusError {
		m.Errors = 1
	}
	switch {
	case s.Cost.Priced:
		m.CostNanos = s.Cost.Nanos
	case s.HasUsage:
		m.Unpriced = 1
	}
	return m
}

// Add returns the field-wise sum m + o.
func (m Measures) Add(o Measures) Measures {
	return Measures{
		Count:     m.Count + o.Count,
		Errors:    m.Errors + o.Errors,
		Usage:     m.Usage.Add(o.Usage),
		CostNanos: m.CostNanos + o.CostNanos,
		Unpriced:  m.Unpriced + o.Unpriced,
	}
}

// Sub returns the field-wise difference m − o.
func (m Measures) Sub(o Measures) Measures {
	return Measures{
		Count:     m.Count - o.Count,
		Errors:    m.Errors - o.Errors,
		Usage:     m.Usage.Sub(o.Usage),
		CostNanos: m.CostNanos - o.CostNanos,
		Unpriced:  m.Unpriced - o.Unpriced,
	}
}

// IsZero reports whether every measure is zero.
func (m Measures) IsZero() bool { return m == Measures{} }

// Aggregate is one rollup row: measures excluding duplicates and the latency
// histogram of all spans. Like Histogram it must not be copied once used; use Clone.
type Aggregate struct {
	Measures  Measures
	Histogram Histogram
}

// AddSpan folds a newly stored span in: its duration always, its measures only
// when it is not flagged usage_duplicate.
func (a *Aggregate) AddSpan(s *trace.Span) {
	a.Histogram.Add(s.DurationNs())
	if !s.Flags.Has(trace.FlagUsageDuplicate) {
		a.Measures = a.Measures.Add(SpanMeasures(s))
	}
}

// Apply adds a (possibly negative) delta to the measures; the histogram is
// never decremented.
func (a *Aggregate) Apply(delta Measures) { a.Measures = a.Measures.Add(delta) }

// Merge adds o to a.
func (a *Aggregate) Merge(o *Aggregate) {
	a.Measures = a.Measures.Add(o.Measures)
	a.Histogram.Merge(&o.Histogram)
}

// Clone returns a deep copy.
func (a *Aggregate) Clone() Aggregate {
	return Aggregate{Measures: a.Measures, Histogram: a.Histogram.Clone()}
}
