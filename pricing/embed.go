// SPDX-License-Identifier: Apache-2.0

// Package pricing embeds the default price table (default.yaml) shipped in the
// binary. Parsing and merging live in internal/adapter/pricingfile.
//
// Usage:
//
//	data := pricing.DefaultYAML()
package pricing

import _ "embed" // for go:embed.

//go:embed default.yaml
var defaultYAML []byte

// DefaultYAML returns a copy of the embedded default price table.
func DefaultYAML() []byte { return append([]byte(nil), defaultYAML...) }
