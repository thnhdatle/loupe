// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"slices"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type Family string

const (
	FamilyGenAI         Family = "gen_ai"
	FamilyOpenInference Family = "openinference"
	FamilyLegacy        Family = "legacy"
)

func Families() []Family {
	return []Family{FamilyGenAI, FamilyOpenInference, FamilyLegacy}
}

func FamilyInclusive(f Family) bool { return !slices.Contains(nonInclusiveFamilies, f) }

var nonInclusiveFamilies = []Family{}

func FamilyOf(key string) Family {
	switch {
	case strings.HasPrefix(key, "gen_ai."):
		return FamilyGenAI
	case strings.HasPrefix(key, "llm.token_count."), strings.HasPrefix(key, "openinference."),
		key == "llm.provider", key == "llm.system", key == "llm.model_name":
		return FamilyOpenInference
	default:
		return FamilyLegacy
	}
}

type Field string

const (
	FieldOperation     Field = "operation"
	FieldProvider      Field = "provider"
	FieldRequestModel  Field = "request_model"
	FieldResponseModel Field = "response_model"
	FieldInputTokens   Field = "input_tokens"
	FieldOutputTokens  Field = "output_tokens"
	FieldCacheRead     Field = "cache_read"
	FieldCacheWrite    Field = "cache_write"
	FieldReasoning     Field = "reasoning"
	FieldTotalTokens   Field = "total_tokens"
	FieldFinishReasons Field = "finish_reasons"
	FieldSession       Field = "session"
	FieldUser          Field = "user"
	FieldTool          Field = "tool"
	FieldAgent         Field = "agent"
	FieldOIKind        Field = "openinference_kind"
)

type Alias struct {
	Field Field
	Keys  []string
}

var aliasTable = []Alias{
	{FieldOperation, []string{"gen_ai.operation.name"}},
	{FieldProvider, []string{"gen_ai.provider.name", "gen_ai.system", "llm.provider", "llm.system"}},
	{FieldRequestModel, []string{"gen_ai.request.model", "llm.request.model", "llm.request.model_name"}},
	{FieldResponseModel, []string{
		"gen_ai.response.model", "llm.model_name", "llm.response.model", "llm.response.model_name",
	}},
	{FieldInputTokens, []string{
		"gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens",
		"llm.token_count.prompt", "llm.usage.prompt_tokens",
	}},
	{FieldOutputTokens, []string{
		"gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens",
		"llm.token_count.completion", "llm.usage.completion_tokens",
	}},
	{FieldCacheRead, []string{
		"gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens",
		"llm.token_count.prompt_details.cache_read",
	}},
	{FieldCacheWrite, []string{
		"gen_ai.usage.cache_write.input_tokens", "gen_ai.usage.cache_creation.input_tokens",
		"gen_ai.usage.cache_creation_input_tokens", "llm.token_count.prompt_details.cache_write",
	}},
	{FieldReasoning, []string{
		"gen_ai.usage.reasoning.output_tokens", "llm.token_count.completion_details.reasoning",
	}},
	{FieldTotalTokens, []string{"gen_ai.usage.total_tokens", "llm.token_count.total", "llm.usage.total_tokens"}},
	{FieldFinishReasons, []string{"gen_ai.response.finish_reasons"}},
	{FieldSession, []string{"gen_ai.conversation.id", "session.id"}},
	{FieldUser, []string{"user.id", "enduser.id"}},
	{FieldTool, []string{"gen_ai.tool.name", "gen_ai.tool.call.id", "tool.name"}},
	{FieldAgent, []string{"gen_ai.agent.name"}},
	{FieldOIKind, []string{"openinference.span.kind"}},
}

var usageFields = []Field{
	FieldInputTokens, FieldOutputTokens, FieldCacheRead, FieldCacheWrite, FieldReasoning, FieldTotalTokens,
}

func Aliases() []Alias {
	out := make([]Alias, len(aliasTable))
	for i, a := range aliasTable {
		out[i] = Alias{Field: a.Field, Keys: slices.Clone(a.Keys)}
	}
	return out
}

func keysOf(f Field) []string {
	for _, a := range aliasTable {
		if a.Field == f {
			return a.Keys
		}
	}
	return nil
}

func IsMappedUsageKey(key string) bool {
	for _, f := range usageFields {
		if slices.Contains(keysOf(f), key) {
			return true
		}
	}
	return false
}

func isUsageNamespace(key string) bool {
	return strings.HasPrefix(key, "gen_ai.usage.") ||
		strings.HasPrefix(key, "llm.token_count.") ||
		strings.HasPrefix(key, "llm.usage.")
}

func UnmappedUsageKeys(attrs []trace.Attr) []string {
	var out []string
	for _, a := range attrs {
		if isUsageNamespace(a.Key) && !IsMappedUsageKey(a.Key) {
			out = append(out, a.Key)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
