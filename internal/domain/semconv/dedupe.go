// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"bytes"
	"slices"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// UsageSpan is the view of a stored span that the usage-duplicate rule needs.
type UsageSpan struct {
	SpanID       trace.SpanID
	ParentSpanID trace.SpanID
	Kind         trace.Kind
	Usage        trace.Usage
	Cost         trace.Cost // the span's own cost, computed at ingest
}

// Contribution is what one span adds to trace, session and rollup aggregates.
// A duplicate contributes nothing except its latency (histograms include it).
type Contribution struct {
	Duplicate bool
	Usage     trace.Usage // tokens of the call, for kept spans
	Cost      trace.Cost  // own cost, or the cost taken from its duplicates
	Unpriced  bool        // kept span with tokens and no effective price
}

// Counted reports whether the span counts as a request or call.
func (c Contribution) Counted() bool { return !c.Duplicate }

// Dedupe applies the usage-duplicate rule to the
// stored spans of one trace and returns one Contribution per input span, in input
// order. The result depends only on the set of spans, not on their order.
//
// For an llm or embedding span S, the nearest same-kind ancestor A is found by
// walking parent ids through the given spans, skipping other kinds; the walk
// stops at a root or at a parent that is not in spans. With A's input + output
// > 0, a member S of A's group is a duplicate when its input and output equal
// A's (rule 1), and every member is a duplicate when the group's input and
// output sum to A's (rule 2). Cache and reasoning counts and, for an unpriced
// kept span, the cost are taken from the duplicates, bottom-up.
func Dedupe(spans []UsageSpan) []Contribution {
	g := newUsageGraph(spans)
	out := make([]Contribution, len(spans))
	for i := range spans {
		out[i] = g.contribution(i)
	}
	return out
}

// effective is a span's call totals after taking values from its duplicates.
type effective struct {
	extras trace.Usage // only CacheRead, CacheWrite, Reasoning are used
	cost   trace.Cost
}

type usageGraph struct {
	spans    []UsageSpan
	ancestor []int   // index of the nearest same-kind ancestor, or -1
	group    [][]int // members of each span's group, sorted by span id
	dup      []bool
	rule2    []bool // the span's group was flagged by rule 2
	eff      []*effective
}

func newUsageGraph(spans []UsageSpan) *usageGraph {
	g := &usageGraph{
		spans:    spans,
		ancestor: make([]int, len(spans)),
		group:    make([][]int, len(spans)),
		dup:      make([]bool, len(spans)),
		rule2:    make([]bool, len(spans)),
		eff:      make([]*effective, len(spans)),
	}
	g.findAncestors()
	g.breakCycles()
	for i, a := range g.ancestor {
		if a >= 0 {
			g.group[a] = append(g.group[a], i)
		}
	}
	for a := range g.group {
		slices.SortFunc(g.group[a], func(x, y int) int {
			return bytes.Compare(spans[x].SpanID[:], spans[y].SpanID[:])
		})
		g.flag(a)
	}
	return g
}

func (g *usageGraph) findAncestors() {
	index := make(map[trace.SpanID]int, len(g.spans))
	for i := range g.spans {
		// On a duplicate span id the first occurrence wins; the store never
		// holds two, so this only guards against malformed input.
		if _, ok := index[g.spans[i].SpanID]; !ok {
			index[g.spans[i].SpanID] = i
		}
	}
	for i := range g.spans {
		g.ancestor[i] = -1
		s := g.spans[i]
		if !s.Kind.HasUsageDedupe() {
			continue
		}
		p := s.ParentSpanID
		for steps := 0; !p.IsZero() && steps < len(g.spans); steps++ {
			j, ok := index[p]
			if !ok || j == i {
				break
			}
			if g.spans[j].Kind == s.Kind {
				g.ancestor[i] = j
				break
			}
			p = g.spans[j].ParentSpanID
		}
	}
}

// breakCycles removes ancestor edges that form a cycle (only possible with
// malformed parent ids). In each cycle the span with the lowest span id loses
// its ancestor, so the result does not depend on input order.
func (g *usageGraph) breakCycles() {
	const (
		unseen = iota
		onPath
		done
	)
	state := make([]int, len(g.spans))
	for start := range g.spans {
		var path []int
		i := start
		for i >= 0 && state[i] == unseen {
			state[i] = onPath
			path = append(path, i)
			i = g.ancestor[i]
		}
		if i >= 0 && state[i] == onPath {
			g.cutCycleAt(i)
		}
		for _, p := range path {
			state[p] = done
		}
	}
}

func (g *usageGraph) cutCycleAt(entry int) {
	lowest := entry
	for i := g.ancestor[entry]; i != entry; i = g.ancestor[i] {
		if bytes.Compare(g.spans[i].SpanID[:], g.spans[lowest].SpanID[:]) < 0 {
			lowest = i
		}
	}
	g.ancestor[lowest] = -1
}

func (g *usageGraph) flag(a int) {
	members := g.group[a]
	au := g.spans[a].Usage
	if len(members) == 0 || au.Input+au.Output <= 0 {
		return
	}
	var in, out int64
	for _, m := range members {
		in += g.spans[m].Usage.Input
		out += g.spans[m].Usage.Output
	}
	if in == au.Input && out == au.Output {
		g.rule2[a] = true
		for _, m := range members {
			g.dup[m] = true
		}
		return
	}
	for _, m := range members {
		if mu := g.spans[m].Usage; mu.Input == au.Input && mu.Output == au.Output {
			g.dup[m] = true
		}
	}
}

func (g *usageGraph) contribution(i int) Contribution {
	if g.dup[i] {
		return Contribution{Duplicate: true}
	}
	s := g.spans[i]
	e := g.effective(i)
	u := s.Usage
	u.CacheRead, u.CacheWrite, u.Reasoning = e.extras.CacheRead, e.extras.CacheWrite, e.extras.Reasoning
	return Contribution{
		Usage:    u,
		Cost:     e.cost,
		Unpriced: !e.cost.Priced && !s.Usage.IsZero(),
	}
}

// effective computes span i's totals bottom-up; the ancestor graph is acyclic
// after breakCycles, so the recursion terminates.
func (g *usageGraph) effective(i int) effective {
	if g.eff[i] != nil {
		return *g.eff[i]
	}
	s := g.spans[i]
	e := effective{extras: s.Usage, cost: s.Cost}
	dups := g.duplicates(i)
	if len(dups) > 0 {
		e = g.takeFromDuplicates(i, dups, e)
	}
	g.eff[i] = &e
	return e
}

func (g *usageGraph) duplicates(i int) []int {
	var dups []int
	for _, m := range g.group[i] {
		if g.dup[m] {
			dups = append(dups, m)
		}
	}
	return dups
}

func (g *usageGraph) takeFromDuplicates(i int, dups []int, e effective) effective {
	if g.rule2[i] {
		var sum trace.Usage
		cost, allPriced := int64(0), true
		for _, m := range dups {
			me := g.effective(m)
			sum = sum.Add(me.extras)
			if me.cost.Priced {
				cost += me.cost.Nanos
			} else if !g.spans[m].Usage.IsZero() {
				allPriced = false
			}
		}
		e.extras = fillExtras(e.extras, sum)
		if !e.cost.Priced && allPriced {
			e.cost = trace.Priced(cost)
		}
		return e
	}
	// Rule 1: values come from the equal duplicate with the lowest span id; the
	// cost from the lowest-id equal duplicate that is priced.
	e.extras = fillExtras(e.extras, g.effective(dups[0]).extras)
	if !e.cost.Priced {
		for _, m := range dups {
			if me := g.effective(m); me.cost.Priced {
				e.cost = me.cost
				break
			}
		}
	}
	return e
}

// fillExtras takes each cache and reasoning count from src when own has none.
func fillExtras(own, src trace.Usage) trace.Usage {
	if own.CacheRead == 0 {
		own.CacheRead = src.CacheRead
	}
	if own.CacheWrite == 0 {
		own.CacheWrite = src.CacheWrite
	}
	if own.Reasoning == 0 {
		own.Reasoning = src.Reasoning
	}
	return own
}
