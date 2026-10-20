// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestEventName_Precedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		field string
		attrs []trace.Attr
		want  string
	}{
		{"field only", "gen_ai.user.message", nil, "gen_ai.user.message"},
		{"attribute only", "", []trace.Attr{sattr("event.name", "gen_ai.choice")}, "gen_ai.choice"},
		{"both prefers field", "a.b", []trace.Attr{sattr("event.name", "c.d")}, "a.b"},
		{
			"fallback otel.event.name", "",
			[]trace.Attr{sattr("otel.event.name", "gen_ai.tool.message")},
			"gen_ai.tool.message",
		},
		{"event.name beats otel.event.name", "", []trace.Attr{
			sattr("otel.event.name", "o"), sattr("event.name", "e"),
		}, "e"},
		{"empty event.name falls through", "", []trace.Attr{sattr("event.name", ""), sattr("otel.event.name", "o")}, "o"},
		{"non-string attribute ignored", "", []trace.Attr{iattr("event.name", 3)}, "3"},
		{"none", "", []trace.Attr{sattr("x", "y")}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &trace.RawLogRecord{EventName: tc.field, Attributes: tc.attrs}
			assert.Equal(t, tc.want, semconv.EventName(rec))
		})
	}
}

func logRec(name string, body trace.Value, attrs ...trace.Attr) *trace.RawLogRecord {
	return &trace.RawLogRecord{
		TraceID: testTrace, SpanID: testSpan, TimeNs: 77, EventName: name, Body: body, Attributes: attrs,
	}
}

func TestTranslateLog_Discards(t *testing.T) {
	t.Parallel()
	noSpan := logRec("gen_ai.user.message", trace.StringValue("x"))
	noSpan.SpanID = trace.SpanID{}
	noTrace := logRec("gen_ai.user.message", trace.StringValue("x"))
	noTrace.TraceID = trace.ID{}
	other := logRec("app.log", trace.StringValue("x"))
	otherNoCtx := logRec("app.log", trace.StringValue("x"))
	otherNoCtx.TraceID = trace.ID{}
	unnamed := logRec("", trace.StringValue("x"))
	tests := []struct {
		name   string
		rec    *trace.RawLogRecord
		reason string
	}{
		{"no span id", noSpan, trace.ReasonNoSpanContext},
		{"no trace id", noTrace, trace.ReasonNoSpanContext},
		{"other name", other, trace.ReasonNotGenAI},
		{"other name without context", otherNoCtx, trace.ReasonNotGenAI},
		{"unnamed", unnamed, trace.ReasonNotGenAI},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parts, reason, keep := semconv.TranslateLog(tc.rec)
			assert.False(t, keep)
			assert.Equal(t, tc.reason, reason)
			assert.Empty(t, parts)
		})
	}
}

func TestTranslateLog_Kept(t *testing.T) {
	t.Parallel()
	lg := trace.SourceLog
	details := trace.MapValue(
		trace.Attr{Key: "gen_ai.output.messages", Value: trace.ArrayValue(trace.StringValue("o"))},
		trace.Attr{Key: "gen_ai.input.messages", Value: trace.StringValue("i")},
		sattr("gen_ai.system_instructions", "sys"), sattr("custom", "c"),
	)
	tests := []struct {
		name string
		rec  *trace.RawLogRecord
		want []partView
	}{
		{
			"user message string body", logRec("gen_ai.user.message", trace.StringValue("hi"),
				sattr("event.name", "gen_ai.user.message"), sattr("gen_ai.system", "openai")),
			[]partView{{0, lg, "gen_ai.user.message", "user", "hi"}},
		},
		{
			"system message", logRec("gen_ai.system.message", trace.StringValue("s")),
			[]partView{{0, lg, "gen_ai.system.message", "system", "s"}},
		},
		{
			"tool message", logRec("gen_ai.tool.message", trace.StringValue("r")),
			[]partView{{0, lg, "gen_ai.tool.message", "tool", "r"}},
		},
		{
			"choice map body", logRec("gen_ai.choice", trace.MapValue(sattr("content", "done"), iattr("index", 0))),
			[]partView{
				{0, lg, "gen_ai.choice", "assistant", "done"},
				{1, lg, "gen_ai.choice", "assistant", `{"index":0}`},
			},
		},
		{
			"role from body field", logRec("gen_ai.custom", trace.MapValue(sattr("role", "judge"), sattr("content", "c"))),
			[]partView{{0, lg, "gen_ai.custom", "judge", "c"}},
		},
		{
			"role from attribute", logRec("gen_ai.custom", trace.StringValue("c"), sattr("role", "judge")),
			[]partView{{0, lg, "gen_ai.custom", "judge", "c"}},
		},
		{
			"operation details map splits per field", logRec(semconv.OperationDetailsEvent, details),
			[]partView{
				{0, lg, "custom", "", "c"},
				{1, lg, "gen_ai.input.messages", "input", "i"},
				{2, lg, "gen_ai.output.messages", "output", `["o"]`},
				{3, lg, "gen_ai.system_instructions", "system", "sys"},
			},
		},
		{
			"operation details non-map body", logRec(semconv.OperationDetailsEvent, trace.StringValue("raw")),
			[]partView{{0, lg, semconv.OperationDetailsEvent, "", "raw"}},
		},
		{"event name from attribute", &trace.RawLogRecord{
			TraceID: testTrace, SpanID: testSpan, Body: trace.StringValue("x"),
			Attributes: []trace.Attr{sattr("otel.event.name", "gen_ai.assistant.message")},
		}, []partView{{0, lg, "gen_ai.assistant.message", "assistant", "x"}}},
		{
			"attributes are content", logRec("gen_ai.choice", trace.Value{}, sattr("finish", "stop")),
			[]partView{{0, lg, "gen_ai.choice", "assistant", `{"finish":"stop"}`}},
		},
		{
			"empty record still records arrival", logRec("gen_ai.user.message", trace.Value{}),
			[]partView{{0, lg, "gen_ai.user.message", "user", ""}},
		},
		{
			"only name carrier attributes", logRec("gen_ai.user.message", trace.Value{},
				sattr("event.name", "gen_ai.user.message")),
			[]partView{{0, lg, "gen_ai.user.message", "user", ""}},
		},
		{
			"array body", logRec("gen_ai.choice", trace.ArrayValue(trace.IntValue(1))),
			[]partView{{0, lg, "gen_ai.choice", "assistant", "[1]"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parts, reason, keep := semconv.TranslateLog(tc.rec)
			require.True(t, keep)
			assert.Empty(t, reason)
			assert.Equal(t, tc.want, views(parts))
			for _, p := range parts {
				assert.Equal(t, testTrace, p.TraceID)
				assert.Equal(t, testSpan, p.SpanID)
				assert.Equal(t, len(p.Body), p.Bytes)
				assert.False(t, p.IsMarker())
			}
		})
	}
}

func TestTranslateLog_TimeNs(t *testing.T) {
	t.Parallel()
	parts, _, keep := semconv.TranslateLog(logRec("gen_ai.user.message", trace.StringValue("x")))
	require.True(t, keep)
	assert.Equal(t, int64(77), parts[0].TimeNs)
}
