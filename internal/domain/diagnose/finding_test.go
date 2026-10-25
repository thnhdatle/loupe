// SPDX-License-Identifier: Apache-2.0

package diagnose_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/diagnose"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestFind_RateLimitRetries(t *testing.T) {
	t.Parallel()
	fs := diagnose.Find(rateLimitedTrace())
	require.Equal(t, []diagnose.Kind{diagnose.KindRateLimit, diagnose.KindRetry, diagnose.KindSlow}, kinds(fs))

	assert.Equal(t, []string{"b1", "b2", "b3"}, fs[0].SpanIDs)
	assert.Equal(t, "3 calls were rate limited: RateLimitError, Rate limit reached for gpt-4o, HTTP 429", fs[0].Text)

	assert.Equal(t, []string{"b1", "b2", "b3", "b4"}, fs[1].SpanIDs)
	assert.Equal(t, "4 calls to gpt-4o under one parent, 3 failed "+
		"(likely retries, 600ms spent on failed calls); the last call succeeded", fs[1].Text)

	assert.Equal(t, []string{"b4"}, fs[2].SpanIDs)
	assert.Contains(t, fs[2].Text, `"chat gpt-4o" took 1.2s`)
}

func TestFind_Errors(t *testing.T) {
	t.Parallel()
	spans := []query.SpanView{
		span("a", "", "agent", trace.KindAgent, at(0, 100)),
		failed(span("b", "a", "lookup", trace.KindTool, at(10, 10)), "KeyError", `{"error.message":"no such user"}`),
		failed(span("c", "a", "parse", trace.KindChain, at(30, 10)), "", `{}`),
		failed(span("d", "a", "parse", trace.KindChain, at(50, 10)), "", `{}`),
		failed(span("e", "a", "fetch", trace.KindOther, at(70, 10)), "", `{"http.status_code":"503"}`),
		failed(span("f", "a", "fetch", trace.KindOther, at(80, 10)), "TooManyRequests", `not json`),
	}
	fs := diagnose.Find(spans)
	require.Len(t, fs, 4)
	assert.Equal(t, diagnose.Finding{
		Kind: diagnose.KindRateLimit, Text: "1 call was rate limited: TooManyRequests", SpanIDs: []string{"f"},
	}, fs[0])
	assert.Equal(t, diagnose.Finding{
		Kind: diagnose.KindToolError, Text: `tool "lookup" failed: KeyError, no such user`, SpanIDs: []string{"b"},
	}, fs[1])
	assert.Equal(t, diagnose.Finding{
		Kind: diagnose.KindError, Text: `"parse" failed 2 times without an error message`, SpanIDs: []string{"c", "d"},
	}, fs[2])
	assert.Equal(t, diagnose.Finding{
		Kind: diagnose.KindError, Text: `"fetch" failed: HTTP 503`, SpanIDs: []string{"e"},
	}, fs[3])
}

func TestFind_HealthyTraceHasNoFindings(t *testing.T) {
	t.Parallel()
	spans := []query.SpanView{
		span("a", "", "agent", trace.KindAgent, at(0, 1000)),
		llm(span("b", "a", "chat", trace.KindLLM, at(0, 300)), "gpt-4o", 500, 50),
		llm(span("c", "a", "chat", trace.KindLLM, at(300, 300)), "gpt-4o", 600, 50),
		llm(span("d", "a", "chat", trace.KindLLM, at(600, 350)), "gpt-4o", 700, 50),
	}
	assert.Empty(t, diagnose.Find(spans), "repeated successful calls are not retries")
	assert.Empty(t, diagnose.Find(nil))
}

func TestFind_LatencyOutlier(t *testing.T) {
	t.Parallel()
	spans := []query.SpanView{
		span("a", "", "agent", trace.KindAgent, at(0, 10_000)),
		span("t1", "a", "search", trace.KindTool, at(0, 200)),
		span("t2", "a", "search", trace.KindTool, at(200, 250)),
		span("t3", "a", "search", trace.KindTool, at(450, 300)),
		span("t4", "a", "fetch", trace.KindTool, at(750, 1500)),
		span("t5", "a", "fetch", trace.KindTool, at(2250, 90)),
	}
	fs := diagnose.Find(spans)
	require.Len(t, fs, 1)
	assert.Equal(t, diagnose.KindSlow, fs[0].Kind)
	assert.Equal(t, []string{"t4"}, fs[0].SpanIDs)
	assert.Equal(t, `"fetch" took 1.5s, 6.0x the median tool span (250ms)`, fs[0].Text)
}

func TestFind_DominantSpan(t *testing.T) {
	t.Parallel()
	spans := []query.SpanView{
		span("a", "", "agent", trace.KindAgent, at(0, 4000)),
		span("b", "a", "retrieve", trace.KindRetrieval, at(0, 3000)),
		span("c", "a", "rerank", trace.KindRerank, at(3000, 400)),
		span("d", "a", "answer", trace.KindLLM, at(3400, 600)),
	}
	fs := diagnose.Find(spans)
	require.Len(t, fs, 1)
	assert.Equal(t, `"retrieve" took 3s, 75% of the trace`, fs[0].Text)
}

func TestFind_TokensAndCost(t *testing.T) {
	t.Parallel()
	spans := []query.SpanView{
		span("a", "", "agent", trace.KindAgent, at(0, 1000)),
		priced(llm(span("b", "a", "plan", trace.KindLLM, at(0, 100)), "m", 1000, 10), 1_000_000),
		priced(llm(span("c", "a", "draft", trace.KindLLM, at(100, 100)), "m2", 1200, 10), 1_000_000),
		priced(llm(span("d", "a", "review", trace.KindLLM, at(200, 100)), "m3", 9000, 10), 8_000_000),
		llm(span("e", "a", "big", trace.KindLLM, at(300, 100)), "m4", 40_000, 10),
		llm(span("f", "a", "small", trace.KindLLM, at(400, 100)), "m5", 1100, 10),
	}
	fs := diagnose.Find(spans)
	require.Equal(t, []diagnose.Kind{diagnose.KindTokenHeavy, diagnose.KindTokenHeavy, diagnose.KindCostShare}, kinds(fs))
	assert.Equal(t, `"review" sent 9000 input tokens, 7.5x the median call (1200)`, fs[0].Text)
	assert.Equal(t, `"big" sent 40000 input tokens, 33.3x the median call (1200)`, fs[1].Text)
	assert.Equal(t, `"review" accounts for 80% of the trace cost ($0.0080 of $0.0100)`, fs[2].Text)
	assert.Equal(t, []string{"d"}, fs[2].SpanIDs)
}

func TestFind_UsageDuplicateIgnored(t *testing.T) {
	t.Parallel()
	dup := llm(span("b", "a", "chat", trace.KindLLM, at(0, 100)), "m", 50_000, 10)
	dup.UsageDuplicate = true
	assert.Empty(t, diagnose.Find([]query.SpanView{span("a", "", "agent", trace.KindAgent, at(0, 1000)), dup}))
}

func TestFind_PrefersStatusMessage(t *testing.T) {
	t.Parallel()
	lookup := span("b", "a", "lookup", trace.KindTool, at(10, 10))
	lookup = failed(lookup, "KeyError", `{"error.message":"from attribute"}`)
	lookup.StatusMessage = "no such user 42"
	fs := diagnose.Find([]query.SpanView{span("a", "", "agent", trace.KindAgent, at(0, 100)), lookup})
	require.Len(t, fs, 1)
	assert.Equal(t, `tool "lookup" failed: KeyError, no such user 42`, fs[0].Text)
}

func TestFind_RateLimitPassedUpIsCountedOnce(t *testing.T) {
	t.Parallel()
	limit := `{"error.message":"Error code: 429 - rate_limit_exceeded"}`
	spans := []query.SpanView{
		failed(span("a", "", "handle request", trace.KindOther, at(0, 300)), "RateLimitError", limit),
		failed(llm(span("b", "a", "chat gpt-4o", trace.KindLLM, at(10, 280)), "gpt-4o", 3, 0), "RateLimitError", limit),
	}
	fs := diagnose.Find(spans)
	require.Equal(t, []diagnose.Kind{diagnose.KindRateLimit}, kinds(fs))
	assert.Equal(t, []string{"b"}, fs[0].SpanIDs)
	assert.Equal(t, "1 call was rate limited: RateLimitError, Error code: 429 - rate_limit_exceeded", fs[0].Text)
}
