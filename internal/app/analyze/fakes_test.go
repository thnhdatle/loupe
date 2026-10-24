// SPDX-License-Identifier: Apache-2.0

package analyze_test

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/thnhdatle/loupe/internal/app/analyze"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

type fakeTraces struct {
	detail  query.TraceDetail
	err     error
	content []bool
}

func (f *fakeTraces) GetTrace(_ context.Context, id trace.ID, inc bool) (query.TraceDetail, error) {
	f.content = append(f.content, inc)
	if f.err != nil {
		return query.TraceDetail{}, f.err
	}
	d := f.detail
	d.Summary.TraceID = id.String()
	return d, nil
}

type fakeLLM struct {
	mu      sync.Mutex
	answer  string
	err     error
	block   bool
	started chan struct{}
	prompts []analyze.Prompt
}

func (f *fakeLLM) Complete(ctx context.Context, p analyze.Prompt) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, p)
	f.mu.Unlock()
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return f.answer, f.err
}

func failedTrace() query.TraceDetail {
	limit := json.RawMessage(`{"http.response.status_code":429}`)
	return query.TraceDetail{
		Summary: query.TraceSummary{RootName: "agent.run", Service: "agent-api", SpanCount: 3, HasError: true},
		Spans: []query.SpanView{
			{
				SpanID: "00f067aa0ba902b7", Name: "agent.run", Kind: trace.KindAgent, Status: trace.StatusOK,
				StartNs: 0, EndNs: 3e9, DurationNs: 3e9,
			},
			{
				SpanID: "53995c3f42cd8ad8", ParentSpanID: "00f067aa0ba902b7", Name: "chat gpt-4o", Kind: trace.KindLLM,
				Model: "gpt-4o", Status: trace.StatusError, ErrorType: "RateLimitError", Attributes: limit,
				StartNs: 1e8, EndNs: 3e8, DurationNs: 2e8,
			},
			{
				SpanID: "6e0c63257de34c92", ParentSpanID: "00f067aa0ba902b7", Name: "chat gpt-4o", Kind: trace.KindLLM,
				Model: "gpt-4o", Status: trace.StatusOK, StartNs: 1e9, EndNs: 2e9, DurationNs: 1e9,
			},
		},
	}
}
