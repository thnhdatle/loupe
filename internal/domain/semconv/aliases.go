// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"slices"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Family is an instrumentation attribute family.
type Family string

// Attribute families, in precedence order.
const (
	FamilyGenAI         Family = "gen_ai"
	FamilyOpenInference Family = "openinference"
	FamilyLegacy        Family = "legacy"
)

// Families lists the families in precedence order.
func Families() []Family {
	return []Family{FamilyGenAI, FamilyOpenInference, FamilyLegacy}
}

// FamilyInclusive reports whether the family's token counts already include
// cache and reasoning tokens. Every family is inclusive until a
// recorded payload proves otherwise; a non-inclusive family has its cache
// tokens added to input and its reasoning tokens added to output.
func FamilyInclusive(f Family) bool { return !slices.Contains(nonInclusiveFamilies, f) }

// nonInclusiveFamilies lists families proven by a recorded payload to report
// cache and reasoning tokens separately from input and output. Empty today.
var nonInclusiveFamilies = []Family{}

// FamilyOf returns the family a mapped attribute key belongs to.
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

// Field is a canonical field of the alias table.
type Field string

// Canonical fields of the alias table.
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

// Alias maps a canonical field to its accepted attribute keys in precedence order.
type Alias struct {
	Field Field
	Keys  []string
}

// aliasTable is the GenAI semantic-convention table plus the OpenInference names llm.request.model_name
// and llm.response.model_name (additions, appended last in their rows). The service field is a resource attribute
// (service.name) and is handled by Translate.
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

// usageFields are the fields whose presence sets Span.HasUsage and that the
// unmapped-key check covers; total tokens are a cross-check only.
var usageFields = []Field{
	FieldInputTokens, FieldOutputTokens, FieldCacheRead, FieldCacheWrite, FieldReasoning, FieldTotalTokens,
}

// Aliases returns a copy of the alias table.
func Aliases() []Alias {
	out := make([]Alias, len(aliasTable))
	for i, a := range aliasTable {
		out[i] = Alias{Field: a.Field, Keys: slices.Clone(a.Keys)}
	}
	return out
}

// keysOf returns the accepted keys of a field in precedence order.
func keysOf(f Field) []string {
	for _, a := range aliasTable {
		if a.Field == f {
			return a.Keys
		}
	}
	return nil
}

// IsMappedUsageKey reports whether key is an accepted usage key of the alias
// table (including the total-token cross-check keys).
func IsMappedUsageKey(key string) bool {
	for _, f := range usageFields {
		if slices.Contains(keysOf(f), key) {
			return true
		}
	}
	return false
}

// isUsageNamespace reports whether key lives in a usage-count namespace.
func isUsageNamespace(key string) bool {
	return strings.HasPrefix(key, "gen_ai.usage.") ||
		strings.HasPrefix(key, "llm.token_count.") ||
		strings.HasPrefix(key, "llm.usage.")
}

// UnmappedUsageKeys returns, sorted and without duplicates, the usage-namespace
// keys (gen_ai.usage.*, llm.token_count.*, llm.usage.*) in attrs that the alias
// table does not map.
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
