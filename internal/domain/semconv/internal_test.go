// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestApplyInclusion_NonInclusiveFamilies(t *testing.T) {
	t.Parallel()
	u := trace.Usage{Input: 100, Output: 50, CacheRead: 30, CacheWrite: 10, Reasoning: 20}
	tests := []struct {
		name         string
		inIncl       bool
		outIncl      bool
		wantIn, want int64
	}{
		{"inclusive", true, true, 100, 50},
		{"input excludes cache", false, true, 140, 50},
		{"output excludes reasoning", true, false, 100, 70},
		{"both", false, false, 140, 70},
	}
	for _, tc := range tests {
		got := applyInclusion(u, tc.inIncl, tc.outIncl)
		assert.Equal(t, tc.wantIn, got.Input, tc.name)
		assert.Equal(t, tc.want, got.Output, tc.name)
		assert.Equal(t, u.CacheRead, got.CacheRead, tc.name)
	}
}

func TestGlobMatch_Patterns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"a.b", "a.b", true},
		{"a.b", "a.bc", false},
		{"a.*", "a.", true},
		{"a.*", "a", false},
		{"a.*.c.*", "a.x.y.c.z", true},
		{"a.*.c", "a.b.cc", false},
		{"*", "", true},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, globMatch(tc.pattern, tc.s), tc.pattern+" "+tc.s)
	}
}
