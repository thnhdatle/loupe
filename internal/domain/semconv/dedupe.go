// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"bytes"
	"slices"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type UsageSpan struct {
	SpanID       trace.SpanID
	ParentSpanID trace.SpanID
	Kind         trace.Kind
	Usage        trace.Usage
	Cost         trace.Cost
}

type Contribution struct {
	Duplicate bool
	Usage     trace.Usage
	Cost      trace.Cost
	Unpriced  bool
}

func (c Contribution) Counted() bool { return !c.Duplicate }

// Dedupe marks usage that nested spans count twice and returns each span's contribution.
func Dedupe(spans []UsageSpan) []Contribution {
	g := newUsageGraph(spans)
	out := make([]Contribution, len(spans))
	for i := range spans {
		out[i] = g.contribution(i)
	}
	return out
}

type effective struct {
	extras trace.Usage
	cost   trace.Cost
}

type usageGraph struct {
	spans    []UsageSpan
	ancestor []int
	group    [][]int
	dup      []bool
	rule2    []bool
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
