// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func aliasKeys(t *testing.T, f semconv.Field) []string {
	t.Helper()
	for _, a := range semconv.Aliases() {
		if a.Field == f {
			return a.Keys
		}
	}
	require.FailNow(t, "field not in alias table", string(f))
	return nil
}

// Rows of the alias table documented in docs/guides/instrumentation.md,
// including the two OpenInference additions (llm.*.model_name).
func TestAliases_Table_MatchesGuide(t *testing.T) {
	t.Parallel()
	want := map[semconv.Field][]string{
		semconv.FieldOperation:    {"gen_ai.operation.name"},
		semconv.FieldProvider:     {"gen_ai.provider.name", "gen_ai.system", "llm.provider", "llm.system"},
		semconv.FieldRequestModel: {"gen_ai.request.model", "llm.request.model", "llm.request.model_name"},
		semconv.FieldResponseModel: {
			"gen_ai.response.model", "llm.model_name", "llm.response.model", "llm.response.model_name",
		},
		semconv.FieldInputTokens: {
			"gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens",
			"llm.token_count.prompt", "llm.usage.prompt_tokens",
		},
		semconv.FieldOutputTokens: {
			"gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens",
			"llm.token_count.completion", "llm.usage.completion_tokens",
		},
		semconv.FieldCacheRead: {
			"gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens",
			"llm.token_count.prompt_details.cache_read",
		},
		semconv.FieldCacheWrite: {
			"gen_ai.usage.cache_write.input_tokens", "gen_ai.usage.cache_creation.input_tokens",
			"gen_ai.usage.cache_creation_input_tokens", "llm.token_count.prompt_details.cache_write",
		},
		semconv.FieldReasoning: {
			"gen_ai.usage.reasoning.output_tokens", "llm.token_count.completion_details.reasoning",
		},
		semconv.FieldTotalTokens:   {"gen_ai.usage.total_tokens", "llm.token_count.total", "llm.usage.total_tokens"},
		semconv.FieldFinishReasons: {"gen_ai.response.finish_reasons"},
		semconv.FieldSession:       {"gen_ai.conversation.id", "session.id"},
		semconv.FieldUser:          {"user.id", "enduser.id"},
		semconv.FieldTool:          {"gen_ai.tool.name", "gen_ai.tool.call.id", "tool.name"},
		semconv.FieldAgent:         {"gen_ai.agent.name"},
		semconv.FieldOIKind:        {"openinference.span.kind"},
	}
	got := semconv.Aliases()
	require.Len(t, got, len(want))
	for _, a := range got {
		assert.Equal(t, want[a.Field], a.Keys, a.Field)
	}
}

func TestAliases_Copy_IsIndependent(t *testing.T) {
	t.Parallel()
	a := semconv.Aliases()
	a[0].Keys[0] = "mutated"
	assert.Equal(t, "gen_ai.operation.name", semconv.Aliases()[0].Keys[0])
}

func TestFamily_Inclusive_AllFamiliesInclusive(t *testing.T) {
	t.Parallel()
	for _, f := range semconv.Families() {
		assert.True(t, semconv.FamilyInclusive(f), f)
	}
}

func TestFamilyOf_Key_Families(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  string
		want semconv.Family
	}{
		{"gen_ai.usage.input_tokens", semconv.FamilyGenAI},
		{"llm.token_count.prompt", semconv.FamilyOpenInference},
		{"openinference.span.kind", semconv.FamilyOpenInference},
		{"llm.model_name", semconv.FamilyOpenInference},
		{"llm.usage.prompt_tokens", semconv.FamilyLegacy},
		{"llm.request.model", semconv.FamilyLegacy},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, semconv.FamilyOf(tc.key), tc.key)
	}
}

func TestTranslate_UsageAliases_EveryKey(t *testing.T) {
	t.Parallel()
	fields := map[semconv.Field]func(u trace.Usage) int64{
		semconv.FieldInputTokens:  func(u trace.Usage) int64 { return u.Input },
		semconv.FieldOutputTokens: func(u trace.Usage) int64 { return u.Output },
		semconv.FieldCacheRead:    func(u trace.Usage) int64 { return u.CacheRead },
		semconv.FieldCacheWrite:   func(u trace.Usage) int64 { return u.CacheWrite },
		semconv.FieldReasoning:    func(u trace.Usage) int64 { return u.Reasoning },
	}
	for field, get := range fields {
		for _, key := range aliasKeys(t, field) {
			t.Run(key, func(t *testing.T) {
				t.Parallel()
				s := semconv.Translate(rawSpan(iattr(key, 7)), semconv.Options{})
				assert.Equal(t, int64(7), get(s.Usage))
				assert.True(t, s.HasUsage)
				assert.Contains(t, attrsJSON(t, s.Attributes), key, "typed-field keys stay in attributes")
			})
		}
	}
}

func TestTranslate_StringAliases_EveryKey(t *testing.T) {
	t.Parallel()
	type getter func(s *trace.Span) string
	fields := map[semconv.Field]getter{
		semconv.FieldProvider:      func(s *trace.Span) string { return s.Provider },
		semconv.FieldRequestModel:  func(s *trace.Span) string { return s.Model },
		semconv.FieldResponseModel: func(s *trace.Span) string { return s.Model },
		semconv.FieldSession:       func(s *trace.Span) string { return s.SessionID },
		semconv.FieldUser:          func(s *trace.Span) string { return s.UserID },
	}
	for field, get := range fields {
		for _, key := range aliasKeys(t, field) {
			t.Run(key, func(t *testing.T) {
				t.Parallel()
				s := semconv.Translate(rawSpan(sattr(key, "val")), semconv.Options{})
				assert.Equal(t, "val", get(&s))
			})
		}
	}
}

func TestTranslate_Precedence_FamiliesCompete(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		attrs []trace.Attr
		want  trace.Usage
	}{
		{"gen_ai over openinference over legacy", []trace.Attr{
			iattr("llm.usage.prompt_tokens", 1), iattr("llm.token_count.prompt", 2), iattr("gen_ai.usage.input_tokens", 3),
		}, trace.Usage{Input: 3}},
		{"new gen_ai name over old gen_ai name", []trace.Attr{
			iattr("gen_ai.usage.prompt_tokens", 1), iattr("gen_ai.usage.input_tokens", 2),
		}, trace.Usage{Input: 2}},
		{"openinference over legacy", []trace.Attr{
			iattr("llm.usage.completion_tokens", 1), iattr("llm.token_count.completion", 2),
		}, trace.Usage{Output: 2}},
		{"unparseable high-precedence falls through", []trace.Attr{
			sattr("gen_ai.usage.input_tokens", "many"), iattr("llm.token_count.prompt", 9),
		}, trace.Usage{Input: 9}},
		{"cache write precedence", []trace.Attr{
			iattr("gen_ai.usage.cache_creation_input_tokens", 1), iattr("gen_ai.usage.cache_write.input_tokens", 2),
		}, trace.Usage{CacheWrite: 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, semconv.Translate(rawSpan(tc.attrs...), semconv.Options{}).Usage)
		})
	}
}

func TestTranslate_Model_ResponseOverRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		attrs []trace.Attr
		want  string
	}{
		{"response wins", []trace.Attr{sattr("gen_ai.request.model", "req"), sattr("gen_ai.response.model", "resp")}, "resp"},
		{"request fallback", []trace.Attr{sattr("llm.request.model", "req")}, "req"},
		{"openinference model_name is response", []trace.Attr{
			sattr("gen_ai.request.model", "req"), sattr("llm.model_name", "oi"),
		}, "oi"},
		{"none", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, semconv.Translate(rawSpan(tc.attrs...), semconv.Options{}).Model)
		})
	}
}

func TestTranslate_IntValues_Forms(t *testing.T) {
	t.Parallel()
	const key = "gen_ai.usage.input_tokens"
	tests := []struct {
		name     string
		val      trace.Value
		want     int64
		hasUsage bool
	}{
		{"int", trace.IntValue(12), 12, true},
		{"integral double", trace.DoubleValue(12), 12, true},
		{"fractional double rejected", trace.DoubleValue(12.5), 0, true},
		{"negative double rejected", trace.DoubleValue(-1), 0, true},
		{"huge double rejected", trace.DoubleValue(1e300), 0, true},
		{"numeric string", trace.StringValue("42"), 42, true},
		{"padded numeric string", trace.StringValue(" 42 "), 42, true},
		{"float string", trace.StringValue("42.0"), 42, true},
		{"fractional string rejected", trace.StringValue("4.5"), 0, true},
		{"junk string rejected", trace.StringValue("abc"), 0, true},
		{"negative int rejected", trace.IntValue(-3), 0, true},
		{"negative string rejected", trace.StringValue("-3"), 0, true},
		{"bool rejected", trace.BoolValue(true), 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := semconv.Translate(rawSpan(trace.Attr{Key: key, Value: tc.val}), semconv.Options{})
			assert.Equal(t, tc.want, s.Usage.Input)
			assert.Equal(t, tc.hasUsage, s.HasUsage)
		})
	}
}

func TestTranslate_TotalTokens_NeverSummed(t *testing.T) {
	t.Parallel()
	raw := rawSpan(iattr("gen_ai.usage.total_tokens", 99), iattr("llm.token_count.total", 99))
	s := semconv.Translate(raw, semconv.Options{})
	assert.True(t, s.Usage.IsZero())
	assert.False(t, s.HasUsage)
	m := attrsJSON(t, s.Attributes)
	assert.InDelta(t, 99, m["gen_ai.usage.total_tokens"], 0)
	assert.InDelta(t, 99, m["llm.token_count.total"], 0)
}

func TestTranslate_Usage_InclusiveFamilyKeepsCounts(t *testing.T) {
	t.Parallel()
	s := semconv.Translate(rawSpan(
		iattr("gen_ai.usage.input_tokens", 100), iattr("gen_ai.usage.cache_read.input_tokens", 60),
		iattr("gen_ai.usage.output_tokens", 50), iattr("gen_ai.usage.reasoning.output_tokens", 20),
	), semconv.Options{})
	assert.Equal(t, trace.Usage{Input: 100, Output: 50, CacheRead: 60, Reasoning: 20}, s.Usage)
}

func TestIsMappedUsageKey_Keys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  string
		want bool
	}{
		{"gen_ai.usage.input_tokens", true},
		{"gen_ai.usage.total_tokens", true},
		{"llm.token_count.completion_details.reasoning", true},
		{"llm.usage.total_tokens", true},
		{"gen_ai.usage.mystery_tokens", false},
		{"gen_ai.request.model", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, semconv.IsMappedUsageKey(tc.key), tc.key)
	}
}

func TestUnmappedUsageKeys_Attrs(t *testing.T) {
	t.Parallel()
	attrs := []trace.Attr{
		iattr("gen_ai.usage.input_tokens", 1),
		iattr("gen_ai.usage.mystery", 1),
		iattr("llm.token_count.prompt_details.audio", 1),
		iattr("llm.token_count.prompt_details.audio", 2),
		iattr("llm.usage.weird", 1),
		iattr("llm.token_count.total", 1),
		sattr("other.key", "x"),
	}
	assert.Equal(t, []string{
		"gen_ai.usage.mystery", "llm.token_count.prompt_details.audio", "llm.usage.weird",
	}, semconv.UnmappedUsageKeys(attrs))
	assert.Empty(t, semconv.UnmappedUsageKeys(nil))
}
