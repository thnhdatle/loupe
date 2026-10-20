// SPDX-License-Identifier: Apache-2.0

// Package pricingfile loads and merges the embedded and user pricing YAML files.
//
// A file is a mapping with one key, prices, holding a list of entries:
//
//	prices:
//	  - match: exact            # exact | glob
//	    provider: openai        # optional
//	    model: gpt-4o
//	    input_per_mtok: 2.50    # USD per million tokens, decimal text, <= 9 decimals
//	    output_per_mtok: 10.00
//	    cache_read_per_mtok: 1.25      # optional; also cache_write_per_mtok, reasoning_per_mtok
//	    effective_from: 2024-10-01T00:00:00Z   # RFC 3339 or a plain date
//	    as_of: 2026-10-01T00:00:00Z            # required in the embedded default only
//	    source: "https://openai.com/api/pricing"
//
// Unknown keys are errors. Prices are parsed from the scalar text, never through
// float64. The embedded default lives in pricing/default.yaml.
//
// Usage:
//
//	table, err := pricingfile.Load(path) // path "" uses the embedded default only
package pricingfile
