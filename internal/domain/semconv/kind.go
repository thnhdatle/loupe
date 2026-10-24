// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"maps"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func OperationKinds() map[string]trace.Kind { return maps.Clone(operationKinds) }

var operationKinds = map[string]trace.Kind{
	"chat":             trace.KindLLM,
	"text_completion":  trace.KindLLM,
	"generate_content": trace.KindLLM,
	"embeddings":       trace.KindEmbedding,
	"execute_tool":     trace.KindTool,
	"invoke_agent":     trace.KindAgent,
	"create_agent":     trace.KindAgent,
	"invoke_workflow":  trace.KindChain,
	"retrieval":        trace.KindRetrieval,
}

func OpenInferenceKinds() map[string]trace.Kind { return maps.Clone(openInferenceKinds) }

var openInferenceKinds = map[string]trace.Kind{
	"LLM":       trace.KindLLM,
	"EMBEDDING": trace.KindEmbedding,
	"TOOL":      trace.KindTool,
	"RETRIEVER": trace.KindRetrieval,
	"RERANKER":  trace.KindRerank,
	"AGENT":     trace.KindAgent,
	"CHAIN":     trace.KindChain,
	"GUARDRAIL": trace.KindGuardrail,
}

// ResolveKind picks the span kind from the operation name, the OpenInference kind, and model and
// usage presence.
func ResolveKind(operation, oiKind string, hasModel, hasUsage bool) trace.Kind {
	if k, ok := operationKinds[operation]; ok {
		return k
	}
	if k, ok := openInferenceKinds[strings.ToUpper(oiKind)]; ok {
		return k
	}
	if hasModel && hasUsage {
		return trace.KindLLM
	}
	return trace.KindOther
}
