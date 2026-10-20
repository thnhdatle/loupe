// SPDX-License-Identifier: Apache-2.0

// Package cost computes span cost in integer nano-USD from decimal prices.
//
// Prices are USD per million tokens parsed from YAML scalar text by ParsePrice
// into integer nano-USD per million tokens, never through float64. A Price is
// one version of a series (entries sharing match, provider and model); a
// PriceTable merges the default and user entries and selects the version that
// applies to a span; a Calculator prices a span with one exact 128-bit
// numerator and one half-up rounding.
//
// Usage:
//
//	in, _ := cost.ParsePrice("2.50") // 2_500_000_000 nano-USD per Mtok
//	out, _ := cost.ParsePrice("10")
//	table, err := cost.NewPriceTable([]cost.Price{{
//		Match: cost.MatchExact, Provider: "openai", Model: "gpt-4o",
//		InputPerMtok: in, OutputPerMtok: out,
//		EffectiveFrom: from, AsOf: asOf, Source: "https://openai.com/api/pricing",
//	}}, nil)
//	if err != nil {
//		return err
//	}
//	calc, _ := cost.NewCalculator(table)
//	res, err := calc.Cost(cost.Input{
//		Provider: "openai", ResponseModel: "gpt-4o", Start: start,
//		Usage: trace.Usage{Input: 1200, Output: 300},
//	})
//	// res.Cost == trace.Priced(6_000_000): 1200·2.5e9 + 300·1e10 = 6e12, / 10⁶
package cost
