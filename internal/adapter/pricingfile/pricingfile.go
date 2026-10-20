// SPDX-License-Identifier: Apache-2.0

package pricingfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/pricing"
)

// ErrInvalidFile reports a pricing file that cannot be parsed or holds an invalid entry.
var ErrInvalidFile = errors.New("pricingfile: invalid pricing file")

type file struct {
	Prices []entry `yaml:"prices"`
}

// entry is one YAML entry; id is an optional human label that is ignored.
type entry struct {
	ID            string `yaml:"id"`
	Match         string `yaml:"match"`
	Provider      string `yaml:"provider"`
	Model         string `yaml:"model"`
	Input         scalar `yaml:"input_per_mtok"`
	Output        scalar `yaml:"output_per_mtok"`
	CacheRead     scalar `yaml:"cache_read_per_mtok"`
	CacheWrite    scalar `yaml:"cache_write_per_mtok"`
	Reasoning     scalar `yaml:"reasoning_per_mtok"`
	EffectiveFrom scalar `yaml:"effective_from"`
	AsOf          scalar `yaml:"as_of"`
	Source        string `yaml:"source"`
}

// scalar keeps the source text of a YAML scalar and whether the key was present.
type scalar struct {
	text string
	set  bool
}

func (s *scalar) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: want a scalar", n.Line)
	}
	s.text, s.set = n.Value, true
	return nil
}

// Parse decodes a pricing file into entries. All problems are reported together,
// wrapping ErrInvalidFile (price syntax errors also wrap the cost errors).
// Table-level rules (match kind, provenance, duplicate versions) are checked by
// cost.NewPriceTable; Load runs both.
func Parse(r io.Reader) ([]cost.Price, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var f file
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %w", ErrInvalidFile, err)
	}
	out := make([]cost.Price, 0, len(f.Prices))
	var errs []error
	for i := range f.Prices {
		p, err := f.Prices[i].price()
		if err != nil {
			errs = append(errs, fmt.Errorf("entry %d (%q): %w", i, f.Prices[i].Model, err))
			continue
		}
		out = append(out, p)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w:\n%w", ErrInvalidFile, errors.Join(errs...))
	}
	return out, nil
}

func (e *entry) price() (cost.Price, error) {
	p := cost.Price{
		Match:    cost.Match(e.Match),
		Provider: e.Provider,
		Model:    e.Model,
		Source:   e.Source,
	}
	var errs []error
	req := func(name string, s scalar) int64 {
		if !s.set {
			errs = append(errs, fmt.Errorf("%s is required", name))
			return 0
		}
		v, err := cost.ParsePrice(s.text)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		return v
	}
	opt := func(name string, s scalar) *int64 {
		if !s.set {
			return nil
		}
		v, err := cost.ParsePrice(s.text)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			return nil
		}
		return &v
	}
	p.InputPerMtok = req("input_per_mtok", e.Input)
	p.OutputPerMtok = req("output_per_mtok", e.Output)
	p.CacheReadPerMtok = opt("cache_read_per_mtok", e.CacheRead)
	p.CacheWritePerMtok = opt("cache_write_per_mtok", e.CacheWrite)
	p.ReasoningPerMtok = opt("reasoning_per_mtok", e.Reasoning)
	for _, t := range []struct {
		name string
		src  scalar
		dst  *time.Time
	}{{"effective_from", e.EffectiveFrom, &p.EffectiveFrom}, {"as_of", e.AsOf, &p.AsOf}} {
		if !t.src.set {
			continue // required-ness is decided by the table (effective_from, default provenance)
		}
		v, err := parseTime(t.src.text)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.name, err))
			continue
		}
		*t.dst = v
	}
	return p, errors.Join(errs...)
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not an RFC 3339 timestamp or YYYY-MM-DD date", s)
	}
	return t, nil
}

// Defaults returns the entries of the embedded default table.
func Defaults() ([]cost.Price, error) {
	p, err := Parse(bytes.NewReader(pricing.DefaultYAML()))
	if err != nil {
		return nil, fmt.Errorf("embedded pricing/default.yaml: %w", err)
	}
	return p, nil
}

// LoadUser reads and parses the user price file at path.
func LoadUser(path string) ([]cost.Price, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the operator chooses pricing.file.
	if err != nil {
		return nil, fmt.Errorf("pricingfile: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	p, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Load builds the price table from the embedded default and, when path is not
// empty, the user file merged on top (user wins, per series).
func Load(path string) (*cost.PriceTable, error) {
	defaults, err := Defaults()
	if err != nil {
		return nil, err
	}
	var user []cost.Price
	if path != "" {
		if user, err = LoadUser(path); err != nil {
			return nil, err
		}
	}
	t, err := cost.NewPriceTable(defaults, user)
	if err != nil {
		return nil, fmt.Errorf("pricingfile: %w", err)
	}
	return t, nil
}
