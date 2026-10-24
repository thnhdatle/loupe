// SPDX-License-Identifier: Apache-2.0

package cost_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const fixturePath = "../../../testdata/cost/cases.yaml"

const fixtureCaseCount = 200

type scalarText string

func (s *scalarText) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: want a scalar, got kind %d", n.Line, n.Kind)
	}
	*s = scalarText(n.Value)
	return nil
}

type fixtureEntry struct {
	ID            string      `yaml:"id"`
	Match         string      `yaml:"match"`
	Provider      string      `yaml:"provider"`
	Model         string      `yaml:"model"`
	Input         scalarText  `yaml:"input_per_mtok"`
	Output        scalarText  `yaml:"output_per_mtok"`
	CacheRead     *scalarText `yaml:"cache_read_per_mtok"`
	CacheWrite    *scalarText `yaml:"cache_write_per_mtok"`
	Reasoning     *scalarText `yaml:"reasoning_per_mtok"`
	EffectiveFrom scalarText  `yaml:"effective_from"`
	AsOf          scalarText  `yaml:"as_of"`
	Source        string      `yaml:"source"`
}

type fixtureUsage struct {
	Input      int64 `yaml:"input"`
	Output     int64 `yaml:"output"`
	CacheRead  int64 `yaml:"cache_read"`
	CacheWrite int64 `yaml:"cache_write"`
	Reasoning  int64 `yaml:"reasoning"`
}

type fixtureCase struct {
	ID           string       `yaml:"id"`
	Kind         string       `yaml:"kind"`
	Provider     string       `yaml:"provider"`
	Model        string       `yaml:"model"`
	RequestModel string       `yaml:"request_model"`
	Start        scalarText   `yaml:"start"`
	Usage        fixtureUsage `yaml:"usage"`
	Expected     struct {
		CostNanos    *int64  `yaml:"cost_nanos"`
		Price        *string `yaml:"price"`
		UsageClamped bool    `yaml:"usage_clamped"`
	} `yaml:"expected"`
	Explanation string `yaml:"explanation"`
}

type fixture struct {
	Prices struct {
		Default []fixtureEntry `yaml:"default"`
		User    []fixtureEntry `yaml:"user"`
	} `yaml:"prices"`
	Cases []fixtureCase `yaml:"cases"`
}

func loadFixture(t *testing.T) *fixture {
	t.Helper()
	b, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	var f fixture
	require.NoError(t, yaml.Unmarshal(b, &f))
	return &f
}

func parseFixtureTime(t *testing.T, s scalarText) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, string(s))
	require.NoError(t, err, "time %q", s)
	return ts
}

func parseOptional(t *testing.T, s *scalarText) *int64 {
	t.Helper()
	if s == nil {
		return nil
	}
	n, err := cost.ParsePrice(string(*s))
	require.NoError(t, err)
	return &n
}

func toPrices(t *testing.T, entries []fixtureEntry) []cost.Price {
	t.Helper()
	out := make([]cost.Price, 0, len(entries))
	for _, e := range entries {
		in, err := cost.ParsePrice(string(e.Input))
		require.NoError(t, err, e.ID)
		outP, err := cost.ParsePrice(string(e.Output))
		require.NoError(t, err, e.ID)
		out = append(out, cost.Price{
			Match: cost.Match(e.Match), Provider: e.Provider, Model: e.Model,
			InputPerMtok: in, OutputPerMtok: outP,
			CacheReadPerMtok:  parseOptional(t, e.CacheRead),
			CacheWritePerMtok: parseOptional(t, e.CacheWrite),
			ReasoningPerMtok:  parseOptional(t, e.Reasoning),
			EffectiveFrom:     parseFixtureTime(t, e.EffectiveFrom),
			AsOf:              parseFixtureTime(t, e.AsOf),
			Source:            e.Source,
		})
	}
	return out
}

func entryKey(origin cost.Origin, match cost.Match, provider, model string, from time.Time) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d", origin, match, strings.ToLower(provider), model, from.UnixNano())
}

func fixtureIDs(t *testing.T, f *fixture) map[string]string {
	t.Helper()
	ids := make(map[string]string)
	for origin, entries := range map[cost.Origin][]fixtureEntry{
		cost.OriginDefault: f.Prices.Default, cost.OriginUser: f.Prices.User,
	} {
		for _, e := range entries {
			k := entryKey(origin, cost.Match(e.Match), e.Provider, e.Model, parseFixtureTime(t, e.EffectiveFrom))
			ids[k] = e.ID
		}
	}
	return ids
}

func TestFixture_Cases_ExactlyTwoHundred(t *testing.T) {
	t.Parallel()
	f := loadFixture(t)
	require.Len(t, f.Cases, fixtureCaseCount)
	seen := make(map[string]bool, len(f.Cases))
	kinds := map[string]int{}
	for _, c := range f.Cases {
		assert.False(t, seen[c.ID], "duplicate case id %s", c.ID)
		seen[c.ID] = true
		kinds[c.Kind]++
		assert.NotEmpty(t, c.Explanation, c.ID)
	}
	assert.Equal(t, fixtureCaseCount, kinds["span"]+kinds["trace"], "every case is a span or a trace case")
	assert.Positive(t, kinds["trace"])
}

func TestCalculator_Cost_Fixture(t *testing.T) {
	t.Parallel()
	f := loadFixture(t)
	table, err := cost.NewPriceTable(toPrices(t, f.Prices.Default), toPrices(t, f.Prices.User))
	require.NoError(t, err)
	calc, err := cost.NewCalculator(table)
	require.NoError(t, err)
	ids := fixtureIDs(t, f)

	var spanCases []fixtureCase
	for _, c := range f.Cases {
		if c.Kind == "span" {
			spanCases = append(spanCases, c)
		}
	}
	require.NotEmpty(t, spanCases)

	for _, c := range spanCases {
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()
			res, err := calc.Cost(cost.Input{
				Provider: c.Provider, ResponseModel: c.Model, RequestModel: c.RequestModel,
				Start: parseFixtureTime(t, c.Start),
				Usage: trace.Usage(c.Usage),
			})
			require.NoError(t, err, c.Explanation)
			if c.Expected.CostNanos == nil {
				assert.False(t, res.Cost.Priced, c.Explanation)
				assert.Nil(t, c.Expected.Price)
				return
			}
			require.True(t, res.Cost.Priced, c.Explanation)
			assert.Equal(t, *c.Expected.CostNanos, res.Cost.Nanos, c.Explanation)
			assert.Equal(t, c.Expected.UsageClamped, res.Flags.Has(trace.FlagUsageClamped), c.Explanation)
			require.NotNil(t, c.Expected.Price)
			p := res.Price
			assert.Equal(t, *c.Expected.Price, ids[entryKey(p.Origin, p.Match, p.Provider, p.Model, p.EffectiveFrom)],
				c.Explanation)
		})
	}
}
