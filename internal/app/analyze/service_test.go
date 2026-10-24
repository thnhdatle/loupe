// SPDX-License-Identifier: Apache-2.0

package analyze_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/analyze"
	"github.com/thnhdatle/loupe/internal/domain/diagnose"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func newService(t *testing.T, tr *fakeTraces, llm *fakeLLM, cfg analyze.Config) *analyze.Service {
	t.Helper()
	if cfg.Timeout == 0 {
		cfg.Timeout = time.Minute
	}
	if cfg.MaxSpans == 0 {
		cfg.MaxSpans = 200
	}
	s, err := analyze.New(tr, llm, cfg)
	require.NoError(t, err)
	return s
}

func TestAnalyze_ValidAnswer(t *testing.T) {
	t.Parallel()
	llm := &fakeLLM{answer: "```json\n" + `{"causes":[{"text":" The provider refused the call (rate limit). ",
		"span_ids":["53995C3F42CD8AD8","ffffffffffffffff","53995c3f42cd8ad8"]}],
		"fixes":[{"text":"Retry with backoff.","span_ids":["53995c3f42cd8ad8","6e0c63257de34c92"]},{"text":"  "}],
		"confidence":"high"}` + "\n```"}
	tr := &fakeTraces{detail: failedTrace()}
	rep, err := newService(t, tr, llm, analyze.Config{}).Analyze(t.Context(), traceID)
	require.NoError(t, err)

	assert.Equal(t, traceID, rep.TraceID)
	assert.Empty(t, rep.Notice)
	require.NotEmpty(t, rep.Findings)
	assert.Equal(t, diagnose.KindRateLimit, rep.Findings[0].Kind)
	assert.Equal(t, []analyze.Point{{
		Text: "The provider refused the call (rate limit).", SpanIDs: []string{"53995c3f42cd8ad8"},
	}}, rep.Causes, "unknown and repeated span ids are dropped")
	assert.Equal(t, []analyze.Point{{
		Text: "Retry with backoff.", SpanIDs: []string{"53995c3f42cd8ad8", "6e0c63257de34c92"},
	}}, rep.Fixes, "empty points are dropped")
	assert.Equal(t, []bool{false}, tr.content)
}

func TestAnalyze_PromptCarriesFindingsAndSpans(t *testing.T) {
	t.Parallel()
	llm := &fakeLLM{answer: `{"causes":[{"text":"x","span_ids":[]}]}`}
	_, err := newService(t, &fakeTraces{detail: failedTrace()}, llm, analyze.Config{}).Analyze(t.Context(), traceID)
	require.NoError(t, err)
	require.Len(t, llm.prompts, 1)
	p := llm.prompts[0]
	assert.Contains(t, p.System, `{"causes":[{"text":"...","span_ids":["..."]}]`)
	assert.Contains(t, p.User, `Trace `+traceID+`: "agent.run", service agent-api, 3 spans`)
	assert.Contains(t, p.User,
		"- [rate_limit] 1 call was rate limited: RateLimitError, HTTP 429 (spans: 53995c3f42cd8ad8)")
	assert.Contains(t, p.User, "53995c3f42cd8ad8 | 00f067aa0ba902b7 | chat gpt-4o | llm | gpt-4o | error | "+
		"100 | 200 | 0 | 0 | - | RateLimitError, HTTP 429\n")
	assert.NotContains(t, p.User, "Stored prompt")
}

func TestAnalyze_InvalidAnswerKeepsFindings(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]string{
		"not json":     "The provider rate limited you.",
		"wrong shape":  `{"causes":"rate limit"}`,
		"empty points": `{"causes":[],"fixes":[{"text":""}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rep, err := newService(t, &fakeTraces{detail: failedTrace()}, &fakeLLM{answer: answer}, analyze.Config{}).
				Analyze(t.Context(), traceID)
			require.NoError(t, err)
			assert.Equal(t, "The model did not return a valid answer. Showing the findings only.", rep.Notice)
			assert.NotEmpty(t, rep.Findings)
			assert.Empty(t, rep.Causes)
			assert.NotNil(t, rep.Causes)
			assert.NotNil(t, rep.Fixes)
		})
	}
}

func TestAnalyze_ModelTimeout(t *testing.T) {
	t.Parallel()
	llm := &fakeLLM{block: true}
	s := newService(t, &fakeTraces{detail: failedTrace()}, llm, analyze.Config{Timeout: 20 * time.Millisecond})
	rep, err := s.Analyze(t.Context(), traceID)
	require.NoError(t, err)
	assert.Equal(t, "The model did not answer within 20ms. Showing the findings only.", rep.Notice)
	assert.NotEmpty(t, rep.Findings)
	assert.Equal(t, 20*time.Millisecond, s.Timeout())
}

func TestAnalyze_ModelError(t *testing.T) {
	t.Parallel()
	llm := &fakeLLM{err: errors.New("llm: status 500")}
	rep, err := newService(t, &fakeTraces{detail: failedTrace()}, llm, analyze.Config{}).Analyze(t.Context(), traceID)
	require.NoError(t, err)
	assert.Equal(t, "The model request failed. Showing the findings only.", rep.Notice)
}

func TestAnalyze_CallerCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	llm := &fakeLLM{block: true, started: make(chan struct{}, 1)}
	s := newService(t, &fakeTraces{detail: failedTrace()}, llm, analyze.Config{})
	go func() {
		<-llm.started
		cancel()
	}()
	_, err := s.Analyze(ctx, traceID)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAnalyze_OneAtATimePerTrace(t *testing.T) {
	t.Parallel()
	llm := &fakeLLM{block: true, started: make(chan struct{}, 1)}
	s := newService(t, &fakeTraces{detail: failedTrace()}, llm, analyze.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := s.Analyze(ctx, traceID)
		done <- err
	}()
	<-llm.started
	_, err := s.Analyze(t.Context(), traceID)
	require.ErrorIs(t, err, analyze.ErrBusy)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	llm.block = false
	llm.answer = `{"causes":[{"text":"ok"}]}`
	_, err = s.Analyze(t.Context(), traceID)
	require.NoError(t, err, "the guard is released")
}

func TestAnalyze_IncludeContentAndLimits(t *testing.T) {
	t.Parallel()
	body := "Summarize my order history"
	d := failedTrace()
	d.Spans[2].Content = append(d.Spans[2].Content, query.ContentPartView{Role: "user", Body: &body})
	tr := &fakeTraces{detail: d}
	llm := &fakeLLM{answer: `{"causes":[{"text":"x"}]}`}
	rep, err := newService(t, tr, llm, analyze.Config{IncludeContent: true, MaxSpans: 2}).Analyze(t.Context(), traceID)
	require.NoError(t, err)
	assert.Equal(t, []bool{true}, tr.content)
	assert.Equal(t, 1, rep.SpansOmitted)
	assert.Contains(t, llm.prompts[0].User, `- 6e0c63257de34c92 user: "Summarize my order history"`)
	assert.Contains(t, llm.prompts[0].User, "1 less relevant spans left out")
}

func TestAnalyze_Errors(t *testing.T) {
	t.Parallel()
	s := newService(t, &fakeTraces{err: trace.ErrNotFound}, &fakeLLM{}, analyze.Config{})
	_, err := s.Analyze(t.Context(), traceID)
	require.ErrorIs(t, err, trace.ErrNotFound)
	_, err = s.Analyze(t.Context(), "nope")
	require.ErrorIs(t, err, trace.ErrInvalidID)

	_, err = analyze.New(nil, &fakeLLM{}, analyze.Config{Timeout: time.Second, MaxSpans: 1})
	require.ErrorIs(t, err, analyze.ErrInvalidConfig)
	_, err = analyze.New(&fakeTraces{}, &fakeLLM{}, analyze.Config{})
	require.ErrorIs(t, err, analyze.ErrInvalidConfig)
}
