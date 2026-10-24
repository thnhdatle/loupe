// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"time"
)

// PriceTable selects the price version that applies to a span.
type PriceTable struct {
	tiers   [2]tier
	entries []Price
}

type seriesKey struct {
	match    Match
	provider string
	model    string
}

type series struct {
	key      seriesKey
	literals int
	versions []Price
}

type tier struct {
	exact map[seriesKey]*series
	globs []*series
}

// NewPriceTable merges the default and user prices into a PriceTable.
func NewPriceTable(defaults, user []Price) (*PriceTable, error) {
	userSeries, err := groupSeries(user, OriginUser)
	if err != nil {
		return nil, err
	}
	defaultSeries, err := groupSeries(defaults, OriginDefault)
	if err != nil {
		return nil, err
	}
	for k := range userSeries {
		delete(defaultSeries, k)
	}
	t := &PriceTable{tiers: [2]tier{newTier(userSeries), newTier(defaultSeries)}}
	for _, m := range []map[seriesKey]*series{userSeries, defaultSeries} {
		for _, s := range m {
			t.entries = append(t.entries, s.versions...)
		}
	}
	slices.SortFunc(t.entries, compareEntries)
	return t, nil
}

// Lookup returns the price that applies to a model at the given start time.
func (t *PriceTable) Lookup(provider, model string, start time.Time) (Price, bool) {
	if model == "" {
		return Price{}, false
	}
	prov := strings.ToLower(provider)
	for i := range t.tiers {
		if p, ok := t.tiers[i].lookup(prov, model, start); ok {
			return *p, true
		}
	}
	return Price{}, false
}

func (t *PriceTable) Entries() []Price { return slices.Clone(t.entries) }

func (tr *tier) lookup(prov, model string, start time.Time) (*Price, bool) {
	provs := []string{prov, ""}
	if prov == "" {
		provs = provs[1:]
	}
	for _, p := range provs {
		if s, ok := tr.exact[seriesKey{match: MatchExact, provider: p, model: model}]; ok {
			if v, ok := s.at(start); ok {
				return v, true
			}
		}
	}
	for _, s := range tr.globs {
		if s.key.provider != "" && s.key.provider != prov {
			continue
		}
		if !MatchGlobPattern(s.key.model, model) {
			continue
		}
		if v, ok := s.at(start); ok {
			return v, true
		}
	}
	return nil, false
}

func (s *series) at(start time.Time) (*Price, bool) {
	i := sort.Search(len(s.versions), func(i int) bool { return s.versions[i].EffectiveFrom.After(start) })
	if i == 0 {
		return nil, false
	}
	return &s.versions[i-1], true
}

func newTier(m map[seriesKey]*series) tier {
	tr := tier{exact: make(map[seriesKey]*series)}
	for k, s := range m {
		if k.match == MatchExact {
			tr.exact[k] = s
		} else {
			tr.globs = append(tr.globs, s)
		}
	}
	slices.SortFunc(tr.globs, compareGlobs)
	return tr
}

func compareGlobs(a, b *series) int {
	if c := cmp.Compare(b.literals, a.literals); c != 0 {
		return c
	}
	if c := cmp.Compare(boolRank(b.key.provider != ""), boolRank(a.key.provider != "")); c != 0 {
		return c
	}
	if c := strings.Compare(a.key.model, b.key.model); c != 0 {
		return c
	}
	return strings.Compare(a.key.provider, b.key.provider)
}

func compareEntries(a, b Price) int {
	return cmp.Or(
		strings.Compare(strings.ToLower(a.Provider), strings.ToLower(b.Provider)),
		strings.Compare(a.Model, b.Model),
		strings.Compare(string(a.Match), string(b.Match)),
		strings.Compare(string(b.Origin), string(a.Origin)),
		a.EffectiveFrom.Compare(b.EffectiveFrom),
	)
}

func groupSeries(entries []Price, origin Origin) (map[seriesKey]*series, error) {
	out := make(map[seriesKey]*series)
	for i := range entries {
		p := copyPrice(&entries[i])
		p.Origin = origin
		if err := validateEntry(&p); err != nil {
			return nil, &EntryError{Origin: origin, Index: i, Model: p.Model, Err: err}
		}
		k := seriesKey{match: p.Match, provider: strings.ToLower(p.Provider), model: p.Model}
		s, ok := out[k]
		if !ok {
			s = &series{key: k, literals: literalCount(p.Model)}
			out[k] = s
		}
		for _, v := range s.versions {
			if v.EffectiveFrom.Equal(p.EffectiveFrom) {
				return nil, &EntryError{Origin: origin, Index: i, Model: p.Model, Err: ErrDuplicateVersion}
			}
		}
		s.versions = append(s.versions, p)
	}
	for _, s := range out {
		slices.SortFunc(s.versions, func(a, b Price) int { return a.EffectiveFrom.Compare(b.EffectiveFrom) })
	}
	return out, nil
}

func validateEntry(p *Price) error {
	switch {
	case !p.Match.IsValid():
		return ErrInvalidMatch
	case p.Model == "":
		return ErrEmptyModel
	case p.InputPerMtok < 0 || p.OutputPerMtok < 0 || isNegative(p.CacheReadPerMtok) ||
		isNegative(p.CacheWritePerMtok) || isNegative(p.ReasoningPerMtok):
		return ErrNegativePrice
	case p.EffectiveFrom.IsZero():
		return ErrMissingEffectiveFrom
	case p.Origin == OriginDefault && (p.Source == "" || p.AsOf.IsZero()):
		return ErrMissingProvenance
	}
	return nil
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isNegative(p *int64) bool { return p != nil && *p < 0 }

func copyPrice(p *Price) Price {
	c := *p
	c.CacheReadPerMtok = copyInt(p.CacheReadPerMtok)
	c.CacheWritePerMtok = copyInt(p.CacheWritePerMtok)
	c.ReasoningPerMtok = copyInt(p.ReasoningPerMtok)
	return c
}

func copyInt(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
