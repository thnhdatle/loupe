// SPDX-License-Identifier: Apache-2.0

package content

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

var prefilterFragments = []string{
	"a", "Z", "0", "1", "9", " ", "-", ".", "_", "@", "+", "(", ")", "\t", "=", "/",
	"sk-", "proj-", "ant-", "ghp_", "gho_", "ghs_", "github_pat_", "AKIA", "xoxb-", "AIza",
	"Bearer ", "bearer\t", "example.com", "user@", "4111 1111 1111 1111", "+14155550123",
	"2026-10-20", "10.0.0.1", "abcdefghijklmnopqrstuvwxyz0123456789", "é", "日本",
}

func TestPresetPrefilter_SameOutputProperty(t *testing.T) {
	t.Parallel()
	var rules []compiledRule
	for _, p := range []string{PresetEmail, PresetPhone, PresetCreditCard, PresetAPIKey} {
		rs, ok := presetRules(p)
		if !ok {
			t.Fatalf("preset %s", p)
		}
		rules = append(rules, rs...)
	}
	rapid.Check(t, func(t *rapid.T) {
		parts := rapid.SliceOfN(rapid.SampledFrom(prefilterFragments), 0, 40).Draw(t, "parts")
		s := strings.Join(parts, "")
		for i := range rules {
			with := rules[i]
			without := with
			without.need = nil
			if got, want := with.apply(s), without.apply(s); got != want {
				t.Fatalf("rule %d on %q: with prefilter %q, without %q", i, s, got, want)
			}
		}
	})
}

func TestPresetPrefilter_Needs(t *testing.T) {
	t.Parallel()
	assert.True(t, needEmail("a@b.co"))
	assert.False(t, needEmail("no at sign"))
	for _, lit := range apiKeyLiterals {
		assert.True(t, needAPIKey("x "+lit+" y"), lit)
	}
	assert.False(t, needAPIKey("sk_ gh_p AKI xo AIz"))
	assert.True(t, needBearer("Bearer x"))
	assert.True(t, needBearer("bearer x"))
	assert.False(t, needBearer("Bear"))
	seven := needDigits(phoneMinDigits)
	assert.True(t, seven("1-2-3-4-5-6-7"))
	assert.False(t, seven("1-2-3-4-5-6"))
	thirteen := needDigits(cardMinDigits)
	assert.True(t, thirteen("4111 1111 1111 1"))
	assert.False(t, thirteen("4111 1111 1111"))
}

func BenchmarkRedact_NoMatch(b *testing.B) {
	r, err := NewRedactor([]string{PresetEmail, PresetAPIKey}, nil)
	if err != nil {
		b.Fatal(err)
	}
	s := strings.Repeat("the quick agent model response tool call result 0123 ", 12)
	for b.Loop() {
		_ = r.Redact(s)
	}
}
