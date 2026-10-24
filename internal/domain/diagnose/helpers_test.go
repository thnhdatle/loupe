// SPDX-License-Identifier: Apache-2.0

package diagnose_test

import (
	"encoding/json"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/diagnose"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const base = int64(1_792_400_000_000_000_000)

type window struct{ startMs, durMs int64 }

func at(startMs, durMs int64) window { return window{startMs, durMs} }

func span(id, parent, name string, kind trace.Kind, w window) query.SpanView {
	start := base + w.startMs*int64(time.Millisecond)
	dur := w.durMs * int64(time.Millisecond)
	return query.SpanView{
		SpanID: id, ParentSpanID: parent, Name: name, Kind: kind, Status: trace.StatusOK,
		StartNs: start, EndNs: start + dur, DurationNs: dur, Attributes: json.RawMessage(`{}`),
	}
}

func failed(s query.SpanView, errType, attrs string) query.SpanView {
	s.Status, s.ErrorType, s.Attributes = trace.StatusError, errType, json.RawMessage(attrs)
	return s
}

func llm(s query.SpanView, model string, in, out int64) query.SpanView {
	s.Model, s.HasUsage, s.Usage = model, true, trace.Usage{Input: in, Output: out}
	return s
}

func priced(s query.SpanView, nanos int64) query.SpanView {
	s.CostNanos = &nanos
	return s
}

// rateLimitedTrace is the demo trace: three refused calls, a successful retry and a tool call.
func rateLimitedTrace() []query.SpanView {
	limit := `{"error.message":"Rate limit reached for gpt-4o","http.response.status_code":429}`
	return []query.SpanView{
		span("a1", "", "agent.run", trace.KindAgent, at(0, 4000)),
		failed(llm(span("b1", "a1", "chat gpt-4o", trace.KindLLM, at(10, 200)), "gpt-4o", 900, 0), "RateLimitError", limit),
		failed(llm(span("b2", "a1", "chat gpt-4o", trace.KindLLM, at(400, 200)), "gpt-4o", 900, 0), "RateLimitError", limit),
		failed(llm(span("b3", "a1", "chat gpt-4o", trace.KindLLM, at(1000, 200)), "gpt-4o", 900, 0), "RateLimitError", limit),
		llm(span("b4", "a1", "chat gpt-4o", trace.KindLLM, at(2000, 1200)), "gpt-4o", 900, 120),
		span("c1", "a1", "search", trace.KindTool, at(3300, 300)),
	}
}

func kinds(fs []diagnose.Finding) []diagnose.Kind {
	out := make([]diagnose.Kind, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Kind)
	}
	return out
}
