// SPDX-License-Identifier: Apache-2.0

package cost_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/thnhdatle/loupe/internal/domain/cost"
)

func TestMatchGlobPattern_Cases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		model   string
		want    bool
	}{
		{name: "literal equal", pattern: "gpt-4o", model: "gpt-4o", want: true},
		{name: "literal differs", pattern: "gpt-4o", model: "gpt-4o-mini", want: false},
		{name: "star suffix", pattern: "gpt-4o*", model: "gpt-4o-2024-08-06", want: true},
		{name: "star matches empty", pattern: "gpt-4o*", model: "gpt-4o", want: true},
		{name: "star prefix", pattern: "*-mini", model: "gpt-4o-mini", want: true},
		{name: "star middle", pattern: "claude-*-haiku", model: "claude-3-5-haiku", want: true},
		{name: "star crosses slash", pattern: "local/*", model: "local/qwen/qwen3-8b", want: true},
		{name: "lone star", pattern: "*", model: "anything", want: true},
		{name: "lone star empty", pattern: "*", model: "", want: true},
		{name: "two stars backtrack", pattern: "a*b*c", model: "aXbYbZc", want: true},
		{name: "two stars fail", pattern: "a*b*c", model: "aXbYbZ", want: false},
		{name: "question one char", pattern: "qwen?-*", model: "qwen2-7b", want: true},
		{name: "question not zero chars", pattern: "qwen?-*", model: "qwen-7b", want: false},
		{name: "question not two chars", pattern: "qwen?-*", model: "qwen25-7b", want: false},
		{name: "question one rune", pattern: "m?del", model: "mödel", want: true},
		{name: "star then multibyte", pattern: "*é", model: "café", want: true},
		{name: "brackets literal", pattern: "m[1]", model: "m[1]", want: true},
		{name: "brackets not a class", pattern: "m[12]", model: "m1", want: false},
		{name: "backslash literal", pattern: `a\*`, model: `a\xyz`, want: true},
		{name: "case sensitive", pattern: "gpt-*", model: "GPT-4", want: false},
		{name: "anchored end", pattern: "gpt", model: "gpt-4", want: false},
		{name: "pattern longer", pattern: "gpt-4o-mini", model: "gpt", want: false},
		{name: "empty pattern empty model", pattern: "", model: "", want: true},
		{name: "trailing stars", pattern: "a**", model: "a", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, cost.MatchGlobPattern(tc.pattern, tc.model))
		})
	}
}
