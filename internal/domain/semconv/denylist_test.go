// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
)

func TestContentKeyRules_Samples_MatchTheirRule(t *testing.T) {
	t.Parallel()
	rules := semconv.ContentKeyRules()
	require.NotEmpty(t, rules)
	for _, r := range rules {
		assert.True(t, semconv.IsContentKey(r.Sample), r.Pattern)
		assert.NotEmpty(t, r.Family, r.Pattern)
	}
}

func TestContentKeyRules_SpecEntries_Present(t *testing.T) {
	t.Parallel()
	patterns := map[string]bool{}
	for _, r := range semconv.ContentKeyRules() {
		patterns[r.Pattern] = true
	}
	for _, want := range []string{
		"gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.system_instructions",
		"gen_ai.tool.call.arguments", "gen_ai.tool.call.result", "gen_ai.prompt", "gen_ai.completion",
		"gen_ai.prompt.*", "gen_ai.completion.*", "input.value", "output.value", "llm.input_messages.*",
		"llm.output_messages.*", "llm.prompt_template.*", "tool_call.function.arguments", "tool.parameters",
		"retrieval.documents.*", "reranker.*.documents.*", "embedding.embeddings.*.text",
	} {
		assert.True(t, patterns[want], want)
	}
}

func TestContentKeyRules_Copy_IsIndependent(t *testing.T) {
	t.Parallel()
	r := semconv.ContentKeyRules()
	r[0].Pattern = "mutated"
	assert.NotEqual(t, "mutated", semconv.ContentKeyRules()[0].Pattern)
}

func TestIsContentKey_Keys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  string
		want bool
	}{
		{"gen_ai.prompt.3.role", true},
		{"gen_ai.completion.0.content", true},
		{"llm.input_messages.12.message.tool_calls.0.tool_call.function.arguments", true},
		{"reranker.input_documents.0.document.content", true},
		{"reranker.query", false},
		{"embedding.embeddings.5.embedding.text", true},
		{"embedding.embeddings.5.embedding.vector", false},
		{"retrieval.documents", false},
		{"gen_ai.usage.prompt_tokens", false},
		{"gen_ai.tool.call.id", false},
		{"gen_ai.request.model", false},
		{"llm.token_count.prompt", false},
		{"input.mime_type", false},
		{"", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, semconv.IsContentKey(tc.key), tc.key)
	}
}
