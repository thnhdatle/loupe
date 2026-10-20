//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// lpe05FixturePath is the LPE-05 fixture, relative to the repository root.
var lpe05FixturePath = filepath.Join("testdata", "cost", "cases.yaml")

// lpe05Text keeps a YAML scalar's source text: prices and timestamps are never
// read through float64 or the YAML timestamp resolver.
type lpe05Text string

func (s *lpe05Text) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: want a scalar, got kind %d", n.Line, n.Kind)
	}
	*s = lpe05Text(n.Value)
	return nil
}

type lpe05Usage struct {
	Input      int64 `yaml:"input"`
	Output     int64 `yaml:"output"`
	CacheRead  int64 `yaml:"cache_read"`
	CacheWrite int64 `yaml:"cache_write"`
	Reasoning  int64 `yaml:"reasoning"`
}

func (u lpe05Usage) usage() trace.Usage { return trace.Usage(u) }

// lpe05Span is one span of a trace case.
type lpe05Span struct {
	ID       string     `yaml:"id"`
	Parent   string     `yaml:"parent"`
	Kind     trace.Kind `yaml:"kind"`
	Provider string     `yaml:"provider"`
	Model    string     `yaml:"model"`
	Start    lpe05Text  `yaml:"start"`
	Usage    lpe05Usage `yaml:"usage"`
}

// lpe05Expected holds the expected values of both case kinds; span cases use
// CostNanos, Price and UsageClamped, trace cases CostNanos and the totals.
type lpe05Expected struct {
	CostNanos     *int64  `yaml:"cost_nanos"`
	Price         *string `yaml:"price"`
	UsageClamped  bool    `yaml:"usage_clamped"`
	UnpricedSpans int     `yaml:"unpriced_spans"`
	InputTokens   int64   `yaml:"input_tokens"`
	OutputTokens  int64   `yaml:"output_tokens"`
	Calls         int     `yaml:"calls"`
}

type lpe05Case struct {
	ID           string        `yaml:"id"`
	Kind         string        `yaml:"kind"` // span or trace
	Provider     string        `yaml:"provider"`
	Model        string        `yaml:"model"`
	RequestModel string        `yaml:"request_model"`
	Start        lpe05Text     `yaml:"start"`
	Usage        lpe05Usage    `yaml:"usage"`
	Spans        []lpe05Span   `yaml:"spans"`
	Expected     lpe05Expected `yaml:"expected"`
	Explanation  string        `yaml:"explanation"`
}

type lpe05Fixture struct {
	Prices struct {
		Default yaml.Node `yaml:"default"`
		User    yaml.Node `yaml:"user"`
	} `yaml:"prices"`
	Cases []lpe05Case `yaml:"cases"`
}

func loadLPE05Fixture(root string) (*lpe05Fixture, error) {
	b, err := os.ReadFile(filepath.Join(root, lpe05FixturePath))
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}
	var f lpe05Fixture
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("decode fixture: %w", err)
	}
	return &f, nil
}

// lpe05Pricing is a calculator over the fixture's price table, loaded by the production pricing
// file parser, and the fixture id of every entry.
type lpe05Pricing struct {
	calc *cost.Calculator
	ids  map[string]string
}

// loadLPE05Pricing feeds each fixture price section, re-encoded as a pricing
// file, through pricingfile.Parse (exact decimal parsing of the scalar text)
// and builds the table with cost.NewPriceTable, as pricingfile.Load does.
func loadLPE05Pricing(f *lpe05Fixture) (*lpe05Pricing, error) {
	p := &lpe05Pricing{ids: make(map[string]string)}
	sections := []struct {
		origin cost.Origin
		node   *yaml.Node
	}{{cost.OriginDefault, &f.Prices.Default}, {cost.OriginUser, &f.Prices.User}}
	parsed := make([][]cost.Price, len(sections))
	for i, s := range sections {
		prices, err := lpe05ParseSection(s.node)
		if err != nil {
			return nil, fmt.Errorf("%s prices: %w", s.origin, err)
		}
		var ids []struct {
			ID string `yaml:"id"`
		}
		if err := s.node.Decode(&ids); err != nil {
			return nil, fmt.Errorf("%s prices: entry ids: %w", s.origin, err)
		}
		if len(ids) != len(prices) {
			return nil, fmt.Errorf("%s prices: %d ids for %d entries", s.origin, len(ids), len(prices))
		}
		for j := range prices {
			prices[j].Origin = s.origin
			p.ids[lpe05PriceKey(&prices[j])] = ids[j].ID
		}
		parsed[i] = prices
	}
	table, err := cost.NewPriceTable(parsed[0], parsed[1])
	if err != nil {
		return nil, fmt.Errorf("price table: %w", err)
	}
	if p.calc, err = cost.NewCalculator(table); err != nil {
		return nil, fmt.Errorf("calculator: %w", err)
	}
	return p, nil
}

func lpe05ParseSection(node *yaml.Node) ([]cost.Price, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(map[string]*yaml.Node{"prices": node}); err != nil {
		return nil, fmt.Errorf("re-encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("re-encode: %w", err)
	}
	prices, err := pricingfile.Parse(&buf)
	if err != nil {
		return nil, fmt.Errorf("pricingfile: %w", err)
	}
	return prices, nil
}

// lpe05PriceKey identifies a price version by the fields a selected Price carries.
func lpe05PriceKey(p *cost.Price) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d", p.Origin, p.Match, strings.ToLower(p.Provider), p.Model,
		p.EffectiveFrom.UnixNano())
}

// id returns the fixture id of the price a priced result used, or "".
func (p *lpe05Pricing) id(res cost.Result) string {
	if !res.Cost.Priced {
		return ""
	}
	return p.ids[lpe05PriceKey(&res.Price)]
}
