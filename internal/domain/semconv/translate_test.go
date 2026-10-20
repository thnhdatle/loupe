// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestTranslate_Identity_CopiedFromRaw(t *testing.T) {
	t.Parallel()
	raw := rawSpan()
	raw.ParentSpanID = trace.SpanID{9}
	raw.Name, raw.StartNs, raw.EndNs = "n", 10, 20
	s := semconv.Translate(raw, semconv.Options{})
	assert.Equal(t, raw.TraceID, s.TraceID)
	assert.Equal(t, raw.SpanID, s.SpanID)
	assert.Equal(t, raw.ParentSpanID, s.ParentSpanID)
	assert.Equal(t, "n", s.Name)
	assert.Equal(t, int64(10), s.StartNs)
	assert.Equal(t, int64(20), s.EndNs)
	assert.JSONEq(t, `{}`, string(s.Attributes))
	assert.Nil(t, s.Content)
}

func TestTranslate_Nil_ReturnsEmptySpan(t *testing.T) {
	t.Parallel()
	s := semconv.Translate(nil, semconv.Options{})
	assert.Equal(t, trace.KindOther, s.Kind)
	assert.JSONEq(t, `{}`, string(s.Attributes))
}

func TestTranslate_Status_FromCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code int32
		want trace.Status
	}{
		{trace.StatusCodeUnset, trace.StatusUnset},
		{trace.StatusCodeOK, trace.StatusOK},
		{trace.StatusCodeError, trace.StatusError},
		{99, trace.StatusUnset},
	}
	for _, tc := range tests {
		raw := rawSpan()
		raw.StatusCode = tc.code
		assert.Equal(t, tc.want, semconv.Translate(raw, semconv.Options{}).Status)
	}
}

func TestTranslate_ErrorType_Sources(t *testing.T) {
	t.Parallel()
	exc := func(typ string) trace.RawEvent {
		return trace.RawEvent{Name: "exception", Attributes: []trace.Attr{sattr("exception.type", typ)}}
	}
	tests := []struct {
		name   string
		attrs  []trace.Attr
		status int32
		events []trace.RawEvent
		want   string
	}{
		{"attribute", []trace.Attr{sattr("error.type", "timeout")}, trace.StatusCodeError, nil, "timeout"},
		{"attribute without error status", []trace.Attr{sattr("error.type", "x")}, trace.StatusCodeOK, nil, "x"},
		{
			"attribute beats event",
			[]trace.Attr{sattr("error.type", "a")},
			trace.StatusCodeError,
			[]trace.RawEvent{exc("b")},
			"a",
		},
		{"event on error", nil, trace.StatusCodeError, []trace.RawEvent{exc("ValueError")}, "ValueError"},
		{"event ignored when not error", nil, trace.StatusCodeOK, []trace.RawEvent{exc("ValueError")}, ""},
		{
			"first typed exception event", nil, trace.StatusCodeError,
			[]trace.RawEvent{{Name: "exception"}, {Name: "other"}, exc("A"), exc("B")},
			"A",
		},
		{"none", nil, trace.StatusCodeError, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := rawSpan(tc.attrs...)
			raw.StatusCode, raw.Events = tc.status, tc.events
			assert.Equal(t, tc.want, semconv.Translate(raw, semconv.Options{}).ErrorType)
		})
	}
}

func TestTranslate_SessionUserProvider_StayInAttributes(t *testing.T) {
	t.Parallel()
	s := semconv.Translate(rawSpan(
		sattr("session.id", "s1"), iattr("user.id", 7), sattr("gen_ai.tool.name", "search"),
		sattr("gen_ai.agent.name", "planner"), sattr("gen_ai.system", "openai"),
	), semconv.Options{})
	assert.Equal(t, "s1", s.SessionID)
	assert.Equal(t, "7", s.UserID)
	assert.Equal(t, "openai", s.Provider)
	m := attrsJSON(t, s.Attributes)
	for _, k := range []string{"session.id", "user.id", "gen_ai.tool.name", "gen_ai.agent.name", "gen_ai.system"} {
		assert.Contains(t, m, k)
	}
}

func TestTranslate_Service_FromResource(t *testing.T) {
	t.Parallel()
	raw := rawSpan()
	assert.Empty(t, semconv.Translate(raw, semconv.Options{}).Service)
	raw.Resource = &trace.RawResource{Attributes: []trace.Attr{sattr("service.name", "triage")}}
	s := semconv.Translate(raw, semconv.Options{})
	assert.Equal(t, "triage", s.Service)
	assert.Equal(t, semconv.ResourceID(raw.Resource), s.ResourceID)
	assert.NotZero(t, s.ResourceID)
}

func TestResourceID_Stability(t *testing.T) {
	t.Parallel()
	a := &trace.RawResource{Attributes: []trace.Attr{sattr("service.name", "x"), sattr("host", "h"), iattr("pid", 1)}}
	b := &trace.RawResource{Attributes: []trace.Attr{iattr("pid", 1), sattr("host", "h"), sattr("service.name", "x")}}
	c := &trace.RawResource{Attributes: []trace.Attr{sattr("service.name", "y")}}
	assert.Equal(t, semconv.ResourceID(a), semconv.ResourceID(b))
	assert.NotEqual(t, semconv.ResourceID(a), semconv.ResourceID(c))
	assert.Zero(t, semconv.ResourceID(nil))

	ra := semconv.CanonicalResource(a)
	assert.Equal(t, semconv.CanonicalResource(b), ra)
	assert.JSONEq(t, `{"host":"h","pid":1,"service.name":"x"}`, string(ra.Attributes))
	assert.Equal(t, trace.Resource{Attributes: []byte(`{}`)}, semconv.CanonicalResource(nil))
	assert.NotZero(t, semconv.ResourceID(&trace.RawResource{}))
}

func TestTranslate_Attributes_CanonicalRegardlessOfOrder(t *testing.T) {
	t.Parallel()
	attrs := []trace.Attr{
		sattr("b", "2"), sattr("a", "1"), iattr("c", 3), sattr("gen_ai.usage.input_tokens", "5"),
		{Key: "m", Value: trace.MapValue(sattr("z", "1"), sattr("y", "2"))},
	}
	rev := make([]trace.Attr, len(attrs))
	for i, a := range attrs {
		rev[len(attrs)-1-i] = a
	}
	s1 := semconv.Translate(rawSpan(attrs...), semconv.Options{})
	s2 := semconv.Translate(rawSpan(rev...), semconv.Options{})
	assert.Equal(t, string(s1.Attributes), string(s2.Attributes))
	want := `{"a":"1","b":"2","c":3,"gen_ai.usage.input_tokens":"5","m":{"y":"2","z":"1"}}`
	assert.JSONEq(t, want, string(s1.Attributes))
	assert.Less(t, strings.Index(string(s1.Attributes), `"a"`), strings.Index(string(s1.Attributes), `"b"`))
}

func TestTranslate_Attributes_ValueTypes(t *testing.T) {
	t.Parallel()
	nan := trace.DoubleValue(0)
	nan.Double /= nan.Double
	s := semconv.Translate(rawSpan(
		sattr("s", "q\"\\\n\t\r\x01é<>"), trace.Attr{Key: "b", Value: trace.BoolValue(true)},
		iattr("i", -5), dattr("d", 1.5), dattr("big", 1e21),
		trace.Attr{Key: "by", Value: trace.Value{Type: trace.ValueBytes, Bytes: []byte("hi")}},
		trace.Attr{Key: "arr", Value: trace.ArrayValue(trace.IntValue(1), trace.StringValue("x"), trace.BoolValue(false))},
		trace.Attr{Key: "nan", Value: nan}, trace.Attr{Key: "empty", Value: trace.Value{}},
		sattr("bad", "a\xffb"), sattr("dup", "first"), sattr("dup", "second"),
	), semconv.Options{})
	m := attrsJSON(t, s.Attributes)
	assert.Equal(t, "q\"\\\n\t\r\x01é<>", m["s"])
	assert.Equal(t, true, m["b"])
	assert.InDelta(t, -5, m["i"], 0)
	assert.InDelta(t, 1.5, m["d"], 0)
	assert.InDelta(t, 1e21, m["big"], 0)
	assert.Equal(t, "aGk=", m["by"])
	assert.Equal(t, []any{float64(1), "x", false}, m["arr"])
	assert.Nil(t, m["nan"])
	assert.Nil(t, m["empty"])
	assert.Equal(t, "a�b", m["bad"])
	assert.Equal(t, "first", m["dup"])
	assert.Contains(t, string(s.Attributes), `"i":-5`)
}

func TestTranslate_Truncation_Utf8Boundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		val     string
		max     int
		want    string
		flagged bool
	}{
		{"fits", "hello", 5, "hello", false},
		{"ascii cut", "hello", 3, "hel", true},
		{"cut inside multibyte backs off", "aé", 2, "a", true},
		{"cut after multibyte", "aéb", 3, "aé", true},
		{"cut inside 4-byte rune", "a😀", 4, "a", true},
		{"unlimited zero", "hello", 0, "hello", false},
		{"unlimited negative", "hello", -1, "hello", false},
		{"cut to nothing", "é", 1, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := semconv.Translate(rawSpan(sattr("k", tc.val)), semconv.Options{MaxAttrBytes: tc.max})
			assert.Equal(t, tc.want, attrsJSON(t, s.Attributes)["k"])
			assert.Equal(t, tc.flagged, s.Flags.Has(trace.FlagAttrTruncated))
		})
	}
}

func TestTranslate_Truncation_NestedAndTypedFields(t *testing.T) {
	t.Parallel()
	raw := rawSpan(
		sattr("gen_ai.response.model", "abcdefgh"),
		trace.Attr{Key: "arr", Value: trace.ArrayValue(trace.StringValue("123456789"))},
	)
	s := semconv.Translate(raw, semconv.Options{MaxAttrBytes: 4})
	assert.Equal(t, "abcd", s.Model)
	assert.Equal(t, []any{"1234"}, attrsJSON(t, s.Attributes)["arr"])
	assert.True(t, s.Flags.Has(trace.FlagAttrTruncated))
}

func TestTranslate_RedactHook_NonContentOnly(t *testing.T) {
	t.Parallel()
	redact := func(s string) string {
		out := []byte(s)
		for i := range out {
			if out[i] == '@' {
				out[i] = '#'
			}
		}
		return string(out)
	}
	raw := rawSpan(sattr("note", "a@b"), sattr("input.value", "c@d"), iattr("n", 1))
	raw.Events = []trace.RawEvent{{Name: "gen_ai.user.message", Attributes: []trace.Attr{sattr("content", "e@f")}}}
	s := semconv.Translate(raw, semconv.Options{RedactValue: redact})
	assert.Equal(t, "a#b", attrsJSON(t, s.Attributes)["note"])
	require.Len(t, s.Content, 2)
	assert.Equal(t, "c@d", string(s.Content[0].Body))
	assert.Equal(t, "e@f", string(s.Content[1].Body))
}

func TestTranslate_RedactThenTruncate(t *testing.T) {
	t.Parallel()
	redact := func(string) string { return "0123456789" }
	s := semconv.Translate(rawSpan(sattr("k", "x")), semconv.Options{RedactValue: redact, MaxAttrBytes: 4})
	assert.Equal(t, "0123", attrsJSON(t, s.Attributes)["k"])
}

func TestTranslate_Content_NeverTruncatedOrInAttributes(t *testing.T) {
	t.Parallel()
	long := string(make([]byte, 5000))
	raw := rawSpan(sattr("input.value", long), sattr("k", "v"))
	s := semconv.Translate(raw, semconv.Options{MaxAttrBytes: 10})
	require.Len(t, s.Content, 1)
	assert.Equal(t, 5000, s.Content[0].Bytes)
	assert.Len(t, s.Content[0].Body, 5000)
	assert.False(t, s.Content[0].Truncated)
	assert.NotContains(t, attrsJSON(t, s.Attributes), "input.value")
}
