// SPDX-License-Identifier: Apache-2.0

package rollup

import "github.com/thnhdatle/loupe/internal/domain/trace"

type Measures struct {
	Count     int64
	Errors    int64
	Usage     trace.Usage
	CostNanos int64
	Unpriced  int64
}

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

func (m Measures) Add(o Measures) Measures {
	return Measures{
		Count:     m.Count + o.Count,
		Errors:    m.Errors + o.Errors,
		Usage:     m.Usage.Add(o.Usage),
		CostNanos: m.CostNanos + o.CostNanos,
		Unpriced:  m.Unpriced + o.Unpriced,
	}
}

func (m Measures) Sub(o Measures) Measures {
	return Measures{
		Count:     m.Count - o.Count,
		Errors:    m.Errors - o.Errors,
		Usage:     m.Usage.Sub(o.Usage),
		CostNanos: m.CostNanos - o.CostNanos,
		Unpriced:  m.Unpriced - o.Unpriced,
	}
}

func (m Measures) IsZero() bool { return m == Measures{} }

// Aggregate is one rollup row: summed measures and a latency histogram.
type Aggregate struct {
	Measures  Measures
	Histogram Histogram
}

func (a *Aggregate) AddSpan(s *trace.Span) {
	a.Histogram.Add(s.DurationNs())
	if !s.Flags.Has(trace.FlagUsageDuplicate) {
		a.Measures = a.Measures.Add(SpanMeasures(s))
	}
}

func (a *Aggregate) Apply(delta Measures) { a.Measures = a.Measures.Add(delta) }

func (a *Aggregate) Merge(o *Aggregate) {
	a.Measures = a.Measures.Add(o.Measures)
	a.Histogram.Merge(&o.Histogram)
}

func (a *Aggregate) Clone() Aggregate {
	return Aggregate{Measures: a.Measures, Histogram: a.Histogram.Clone()}
}
