// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"time"
)

// PriceTable selects the price version that applies to a span. It is
// immutable after NewPriceTable and safe for concurrent use.
type PriceTable struct {
	tiers   [2]tier // index 0: user, 1: default; user series win before specificity
	entries []Price
}

// seriesKey identifies a series; the provider is lower-cased because provider
// names are compared case-insensitively.
type seriesKey struct {
	match    Match
	provider string
	model    string
}

// series is every version of one (match, provider, model), sorted by EffectiveFrom.
type series struct {
	key      seriesKey
	literals int
	versions []Price
}

// tier holds the series of one origin: exact series by (provider, model) and
// glob series in selection order.
type tier struct {
	exact map[seriesKey]*series
	globs []*series
}

// NewPriceTable builds a table from the embedded default entries and the user
// file's entries. Origin is set from the argument each entry came from. A user
// series replaces the default series with the same (match, provider, model)
// entirely. Entries are copied, including the optional prices.
//
// It returns an *EntryError for an invalid match kind, an empty model, a
// negative price, a zero EffectiveFrom, a default entry without Source or AsOf,
// or two versions of one series with the same EffectiveFrom.
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

// Lookup returns the price version for a span of provider and pricing model
// that started at start, and false when the model is unpriced.
//
// Selection: only series with a version whose EffectiveFrom ≤ start are
// candidates; user series beat default series; within one origin an exact
// series beats any glob, a glob with more literal characters beats one with
// fewer, and a provider-specific series beats a provider-less one at equal
// specificity; remaining ties between globs go to the pattern, then the
// provider, that sorts first byte-wise. The version is the one with the latest
// EffectiveFrom ≤ start. A provider-specific series applies only when its
// provider equals provider ignoring case; an empty model is never priced.
//
// The returned Price shares its optional price pointers with the table; callers
// must not modify the values they point to.
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

// Entries returns every entry in effect after the user merge, ordered by
// provider, model, match, origin and EffectiveFrom (for `loupe pricing list`).
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

// at returns the version with the latest EffectiveFrom ≤ start.
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

// compareGlobs orders glob series by selection precedence: more literal
// characters, then provider-specific, then pattern, then provider.
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
		strings.Compare(string(b.Origin), string(a.Origin)), // user before default
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
