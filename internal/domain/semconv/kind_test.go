// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestResolveKind_Rules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		op, oi             string
		hasModel, hasUsage bool
		want               trace.Kind
	}{
		{"chat", "chat", "", false, false, trace.KindLLM},
		{"text_completion", "text_completion", "", false, false, trace.KindLLM},
		{"generate_content", "generate_content", "", false, false, trace.KindLLM},
		{"embeddings", "embeddings", "", false, false, trace.KindEmbedding},
		{"execute_tool", "execute_tool", "", false, false, trace.KindTool},
		{"invoke_agent", "invoke_agent", "", false, false, trace.KindAgent},
		{"create_agent", "create_agent", "", false, false, trace.KindAgent},
		{"invoke_workflow", "invoke_workflow", "", false, false, trace.KindChain},
		{"retrieval", "retrieval", "", false, false, trace.KindRetrieval},
		{"operation beats openinference", "embeddings", "LLM", true, true, trace.KindEmbedding},
		{"oi LLM", "", "LLM", false, false, trace.KindLLM},
		{"oi EMBEDDING", "", "EMBEDDING", false, false, trace.KindEmbedding},
		{"oi TOOL", "", "TOOL", false, false, trace.KindTool},
		{"oi RETRIEVER", "", "RETRIEVER", false, false, trace.KindRetrieval},
		{"oi RERANKER", "", "RERANKER", false, false, trace.KindRerank},
		{"oi AGENT", "", "AGENT", false, false, trace.KindAgent},
		{"oi CHAIN", "", "CHAIN", false, false, trace.KindChain},
		{"oi GUARDRAIL", "", "GUARDRAIL", false, false, trace.KindGuardrail},
		{"oi lower case", "", "tool", false, false, trace.KindTool},
		{"unknown operation falls to openinference", "weird", "AGENT", false, false, trace.KindAgent},
		{"fallback llm", "", "", true, true, trace.KindLLM},
		{"fallback model only", "", "", true, false, trace.KindOther},
		{"fallback usage only", "", "", false, true, trace.KindOther},
		{"fallback nothing", "", "", false, false, trace.KindOther},
		{"unknown oi kind", "", "UNKNOWN", false, false, trace.KindOther},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, semconv.ResolveKind(tc.op, tc.oi, tc.hasModel, tc.hasUsage))
		})
	}
}

func TestKindTables_Copies(t *testing.T) {
	t.Parallel()
	ops := semconv.OperationKinds()
	assert.Len(t, ops, 9)
	delete(ops, "chat")
	assert.Contains(t, semconv.OperationKinds(), "chat")
	assert.Len(t, semconv.OpenInferenceKinds(), 8)
}

func TestTranslate_Kind_DecidedByOperationNotSpanNameOrSpanKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		spanName string
		spanKind int32
		op       string
		want     trace.Kind
	}{
		{"triage route request server span chat", "route request", 2, "chat", trace.KindLLM},
		{"triage embeddings server span", "embeddings text-embedding-3-small", 2, "embeddings", trace.KindEmbedding},
		{"client span named like tool but chat", "execute_tool x", 3, "chat", trace.KindLLM},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := rawSpan(sattr("gen_ai.operation.name", tc.op))
			raw.Name, raw.SpanKind = tc.spanName, tc.spanKind
			assert.Equal(t, tc.want, semconv.Translate(raw, semconv.Options{}).Kind)
		})
	}
}

func TestTranslate_Kind_Fallback(t *testing.T) {
	t.Parallel()
	llm := semconv.Translate(rawSpan(sattr("llm.model_name", "m"), iattr("llm.token_count.prompt", 1)), semconv.Options{})
	assert.Equal(t, trace.KindLLM, llm.Kind)
	other := semconv.Translate(rawSpan(sattr("llm.model_name", "m")), semconv.Options{})
	assert.Equal(t, trace.KindOther, other.Kind)
}
