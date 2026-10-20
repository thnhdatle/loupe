// SPDX-License-Identifier: Apache-2.0

package semconv

import "slices"

// Part roles used by the denylist and event names.
const (
	roleInput     = "input"
	roleOutput    = "output"
	roleSystem    = "system"
	roleTool      = "tool"
	roleUser      = "user"
	roleAssistant = "assistant"
	keyRole       = "role"
)

// ContentKeyRule is one entry of the content-key denylist. Pattern is an exact
// key or a glob in which `*` matches any run of characters (dots included).
type ContentKeyRule struct {
	Family  Family
	Pattern string
	Sample  string // a concrete key that matches Pattern
	Role    string // default part role for keys matching the rule; may be empty
}

// contentRules is the content-key denylist (listed in docs/guides/privacy.md).
var contentRules = []ContentKeyRule{
	{FamilyGenAI, "gen_ai.input.messages", "gen_ai.input.messages", roleInput},
	{FamilyGenAI, "gen_ai.output.messages", "gen_ai.output.messages", roleOutput},
	{FamilyGenAI, "gen_ai.system_instructions", "gen_ai.system_instructions", roleSystem},
	{FamilyGenAI, "gen_ai.tool.call.arguments", "gen_ai.tool.call.arguments", roleTool},
	{FamilyGenAI, "gen_ai.tool.call.result", "gen_ai.tool.call.result", roleTool},
	{FamilyGenAI, "gen_ai.prompt", "gen_ai.prompt", roleInput},
	{FamilyGenAI, "gen_ai.completion", "gen_ai.completion", roleOutput},
	{FamilyLegacy, "gen_ai.prompt.*", "gen_ai.prompt.0.content", ""},
	{FamilyLegacy, "gen_ai.completion.*", "gen_ai.completion.0.content", ""},
	{FamilyOpenInference, "input.value", "input.value", roleInput},
	{FamilyOpenInference, "output.value", "output.value", roleOutput},
	{FamilyOpenInference, "llm.input_messages.*", "llm.input_messages.0.message.content", ""},
	{FamilyOpenInference, "llm.output_messages.*", "llm.output_messages.0.message.content", ""},
	{FamilyOpenInference, "llm.prompt_template.*", "llm.prompt_template.template", ""},
	{FamilyOpenInference, "llm.prompts.*", "llm.prompts.0.prompt.text", roleInput},
	{FamilyOpenInference, "tool_call.function.arguments", "tool_call.function.arguments", roleTool},
	{FamilyOpenInference, "tool.parameters", "tool.parameters", roleTool},
	{FamilyOpenInference, "retrieval.documents.*", "retrieval.documents.0.document.content", ""},
	{FamilyOpenInference, "reranker.*.documents.*", "reranker.query.documents.0.content", ""},
	{FamilyOpenInference, "reranker.*_documents.*", "reranker.input_documents.0.document.content", ""},
	{FamilyOpenInference, "embedding.embeddings.*.text", "embedding.embeddings.0.embedding.text", ""},
}

// ContentKeyRules returns a copy of the content-key denylist.
func ContentKeyRules() []ContentKeyRule { return slices.Clone(contentRules) }

// IsContentKey reports whether the attribute key carries content (prompts,
// responses, tool arguments or results, documents) that the content policy handles.
func IsContentKey(key string) bool {
	_, ok := ruleFor(key)
	return ok
}

// ruleFor returns the first rule matching key.
func ruleFor(key string) (ContentKeyRule, bool) {
	for _, r := range contentRules {
		if globMatch(r.Pattern, key) {
			return r, true
		}
	}
	return ContentKeyRule{}, false
}

// globMatch matches s against pattern, where `*` matches any run of characters.
func globMatch(pattern, s string) bool {
	star := -1
	p, i, mark := 0, 0, 0
	for i < len(s) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, i
			p++
		case p < len(pattern) && pattern[p] == s[i]:
			p++
			i++
		case star >= 0:
			mark++
			i, p = mark, star+1
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
