// SPDX-License-Identifier: Apache-2.0

package diagnose_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/diagnose"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestTable_AllSpans(t *testing.T) {
	t.Parallel()
	spans := rateLimitedTrace()
	spans[0], spans[5] = spans[5], spans[0]
	rows, omitted := diagnose.Table(spans, nil, 200)
	assert.Zero(t, omitted)
	require.Len(t, rows, 6)
	assert.Equal(t, "a1", rows[0].SpanID, "ordered by start")
	assert.Zero(t, rows[0].StartNs, "relative to the trace start")
	assert.Equal(t, diagnose.Row{
		SpanID: "b1", ParentSpanID: "a1", Name: "chat gpt-4o", Kind: trace.KindLLM, Model: "gpt-4o",
		Status: trace.StatusError, StartNs: 10_000_000, DurationNs: 200_000_000, InputTokens: 900,
		Error: "RateLimitError, Rate limit reached for gpt-4o, HTTP 429",
	}, rows[1])
}

func TestTable_KeepsCitedAndFailedSpans(t *testing.T) {
	t.Parallel()
	spans := []query.SpanView{
		span("root", "", "agent", trace.KindAgent, at(0, 9000)),
		span("long", "root", "slow step", trace.KindChain, at(10, 5000)),
		span("short", "root", "quick step", trace.KindChain, at(20, 10)),
		failed(span("bad", "root", "broken", trace.KindTool, at(30, 10)), "E", `{}`),
		span("cited", "root", "cited", trace.KindTool, at(40, 1)),
	}
	findings := []diagnose.Finding{{Kind: diagnose.KindSlow, SpanIDs: []string{"cited"}}}
	rows, omitted := diagnose.Table(spans, findings, 3)
	assert.Equal(t, 2, omitted)
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.SpanID)
	}
	assert.Equal(t, []string{"root", "bad", "cited"}, ids)

	rows, omitted = diagnose.Table(spans, findings, 4)
	assert.Equal(t, 1, omitted)
	assert.Equal(t, "long", rows[1].SpanID, "then the longest spans")
}

func TestTable_ContentBounded(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 5000)
	short := "hello"
	s := span("a", "", "chat", trace.KindLLM, at(0, 10))
	s.Content = []query.ContentPartView{
		{Role: "system"},
		{Role: "user", Body: &short},
		{Role: "assistant", Body: &long},
	}
	spans := make([]query.SpanView, 0, 21)
	spans = append(spans, s)
	for i := range 20 {
		c := span(string(rune('b'+i)), "a", "chat", trace.KindLLM, at(int64(i+1), 10))
		c.Content = []query.ContentPartView{{Role: "user", Body: &long}}
		spans = append(spans, c)
	}
	rows, _ := diagnose.Table(spans, nil, 200)
	require.Len(t, rows[0].Content, 2, "markers without a body are skipped")
	assert.Equal(t, diagnose.Message{Role: "user", Text: "hello"}, rows[0].Content[0])
	assert.Equal(t, 2001, utf8.RuneCountInString(rows[0].Content[1].Text))
	assert.True(t, strings.HasSuffix(rows[0].Content[1].Text, "…"))

	total := 0
	for _, r := range rows {
		for _, m := range r.Content {
			total += utf8.RuneCountInString(strings.TrimSuffix(m.Text, "…"))
		}
	}
	assert.Equal(t, 24_000, total, "content stops at the trace budget")
	assert.Empty(t, rows[len(rows)-1].Content)
}
