// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type partView struct {
	Part   int
	Source trace.ContentSource
	Key    string
	Role   string
	Body   string
}

func views(parts []trace.ContentPart) []partView {
	out := make([]partView, len(parts))
	for i, p := range parts {
		out[i] = partView{p.Part, p.Source, p.Key, p.Role, string(p.Body)}
	}
	return out
}

func TestTranslate_Content_AttributeFamilies(t *testing.T) {
	t.Parallel()
	attr := trace.SourceAttribute
	tests := []struct {
		name  string
		attrs []trace.Attr
		want  []partView
	}{
		{"openinference messages with roles", []trace.Attr{
			sattr("llm.input_messages.1.message.content", "q"), sattr("llm.input_messages.0.message.role", "system"),
			sattr("llm.input_messages.0.message.content", "sys"), sattr("llm.input_messages.1.message.role", "user"),
			sattr("llm.output_messages.0.message.content", "a"), sattr("llm.output_messages.0.message.role", "assistant"),
		}, []partView{
			{0, attr, "llm.input_messages.0.message.content", "system", "sys"},
			{1, attr, "llm.input_messages.1.message.content", "user", "q"},
			{2, attr, "llm.output_messages.0.message.content", "assistant", "a"},
		}},
		{"natural index order 2 before 10", []trace.Attr{
			sattr("llm.input_messages.10.message.content", "ten"), sattr("llm.input_messages.2.message.content", "two"),
		}, []partView{
			{0, attr, "llm.input_messages.2.message.content", "", "two"},
			{1, attr, "llm.input_messages.10.message.content", "", "ten"},
		}},
		{"role only message keeps the role part", []trace.Attr{
			sattr("llm.input_messages.0.message.role", "user"),
		}, []partView{{0, attr, "llm.input_messages.0.message.role", "user", "user"}}},
		{"traceloop prompt and completion", []trace.Attr{
			sattr("gen_ai.prompt.0.content", "ping"), sattr("gen_ai.prompt.0.role", "user"),
			sattr("gen_ai.completion.0.content", "pong"), sattr("gen_ai.completion.0.role", "assistant"),
		}, []partView{
			{0, attr, "gen_ai.completion.0.content", "assistant", "pong"},
			{1, attr, "gen_ai.prompt.0.content", "user", "ping"},
		}},
		{"genai semantic convention keys", []trace.Attr{
			sattr("gen_ai.input.messages", "[]"), sattr("gen_ai.output.messages", "[]"),
			sattr("gen_ai.system_instructions", "sys"), sattr("gen_ai.tool.call.arguments", "{}"),
			sattr("gen_ai.tool.call.result", "ok"),
		}, []partView{
			{0, attr, "gen_ai.input.messages", "input", "[]"},
			{1, attr, "gen_ai.output.messages", "output", "[]"},
			{2, attr, "gen_ai.system_instructions", "system", "sys"},
			{3, attr, "gen_ai.tool.call.arguments", "tool", "{}"},
			{4, attr, "gen_ai.tool.call.result", "tool", "ok"},
		}},
		{"openinference values and tools", []trace.Attr{
			sattr("input.value", "i"), sattr("output.value", "o"), sattr("tool.parameters", "{}"),
			sattr("tool_call.function.arguments", "{}"), sattr("retrieval.documents.0.document.content", "doc"),
		}, []partView{
			{0, attr, "input.value", "input", "i"},
			{1, attr, "output.value", "output", "o"},
			{2, attr, "retrieval.documents.0.document.content", "", "doc"},
			{3, attr, "tool.parameters", "tool", "{}"},
			{4, attr, "tool_call.function.arguments", "tool", "{}"},
		}},
		{"structured values become json", []trace.Attr{
			{Key: "gen_ai.input.messages", Value: trace.ArrayValue(trace.MapValue(sattr("role", "user")))},
			iattr("input.value", 5),
		}, []partView{
			{0, attr, "gen_ai.input.messages", "input", `[{"role":"user"}]`},
			{1, attr, "input.value", "input", "5"},
		}},
		{"non-numeric index is not a message", []trace.Attr{
			sattr("gen_ai.prompt.template", "t"),
		}, []partView{{0, attr, "gen_ai.prompt.template", "", "t"}}},
		{"message key without tail", []trace.Attr{
			sattr("llm.input_messages.0", "x"), sattr("llm.input_messages.", "y"),
		}, []partView{
			{0, attr, "llm.input_messages.", "", "y"},
			{1, attr, "llm.input_messages.0", "", "x"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := semconv.Translate(rawSpan(tc.attrs...), semconv.Options{})
			assert.Equal(t, tc.want, views(s.Content))
			for _, p := range s.Content {
				assert.Equal(t, len(p.Body), p.Bytes)
				assert.Equal(t, testTrace, p.TraceID)
				assert.Equal(t, testSpan, p.SpanID)
				assert.Zero(t, p.TimeNs)
			}
		})
	}
}

func TestTranslate_Content_SpanEvents(t *testing.T) {
	t.Parallel()
	ev := trace.SourceEvent
	raw := rawSpan()
	raw.Events = []trace.RawEvent{
		{Name: "gen_ai.system.message", TimeNs: 1, Attributes: []trace.Attr{
			sattr("gen_ai.system", "openai"), sattr("content", "be brief"),
		}},
		{Name: "gen_ai.user.message", TimeNs: 2, Attributes: []trace.Attr{sattr("content", "hi")}},
		{Name: "app.event", TimeNs: 3, Attributes: []trace.Attr{sattr("content", "not gen_ai")}},
		{Name: "gen_ai.assistant.message", TimeNs: 4, Attributes: []trace.Attr{
			sattr("content", "calling"), {Key: "tool_calls", Value: trace.ArrayValue(trace.StringValue("t1"))},
		}},
		{Name: "gen_ai.tool.message", TimeNs: 5, Attributes: []trace.Attr{sattr("content", "result"), sattr("role", "tool")}},
		{Name: "gen_ai.choice", TimeNs: 6, Attributes: []trace.Attr{
			iattr("index", 0), {Key: "message", Value: trace.MapValue(sattr("content", "done"))},
		}},
		{Name: "gen_ai.custom", TimeNs: 7, Attributes: []trace.Attr{sattr("role", "critic"), sattr("content", "c")}},
		{Name: "gen_ai.empty", TimeNs: 8},
	}
	s := semconv.Translate(raw, semconv.Options{})
	want := []partView{
		{0, ev, "gen_ai.system.message", "system", "be brief"},
		{1, ev, "gen_ai.user.message", "user", "hi"},
		{2, ev, "gen_ai.assistant.message", "assistant", "calling"},
		{3, ev, "gen_ai.assistant.message", "assistant", `{"tool_calls":["t1"]}`},
		{4, ev, "gen_ai.tool.message", "tool", "result"},
		{5, ev, "gen_ai.choice", "assistant", `{"index":0,"message":{"content":"done"}}`},
		{6, ev, "gen_ai.custom", "critic", "c"},
		{7, ev, "gen_ai.empty", "", ""},
	}
	assert.Equal(t, want, views(s.Content))
	assert.Equal(t, int64(1), s.Content[0].TimeNs)
	assert.NotNil(t, s.Content[7].Body, "an empty event is a part with an empty body, not a marker")
	assert.False(t, s.Content[7].IsMarker())
	assert.JSONEq(t, `{}`, string(s.Attributes), "events never reach attributes")
}

func TestTranslate_Content_AttributesBeforeEvents(t *testing.T) {
	t.Parallel()
	raw := rawSpan(sattr("input.value", "attr"))
	raw.Events = []trace.RawEvent{{Name: "gen_ai.user.message", Attributes: []trace.Attr{sattr("content", "ev")}}}
	got := semconv.Translate(raw, semconv.Options{}).Content
	require.Len(t, got, 2)
	assert.Equal(t, trace.SourceAttribute, got[0].Source)
	assert.Equal(t, trace.SourceEvent, got[1].Source)
	assert.Equal(t, 1, got[1].Part)
}

func TestTranslate_Content_OrderDeterministic(t *testing.T) {
	t.Parallel()
	attrs := []trace.Attr{
		sattr("llm.input_messages.3.message.content", "c"), sattr("input.value", "i"),
		sattr("llm.input_messages.0.message.content", "a"), sattr("output.value", "o"),
	}
	rev := []trace.Attr{attrs[3], attrs[2], attrs[1], attrs[0]}
	a := semconv.Translate(rawSpan(attrs...), semconv.Options{}).Content
	b := semconv.Translate(rawSpan(rev...), semconv.Options{}).Content
	assert.Equal(t, a, b)
}
