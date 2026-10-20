// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"math"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestConvert_AnyValueTypes(t *testing.T) {
	t.Parallel()
	anyOf := func(v any) *commonpb.AnyValue {
		switch x := v.(type) {
		case bool:
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: x}}
		case int64:
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: x}}
		case float64:
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: x}}
		case []byte:
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: x}}
		}
		return &commonpb.AnyValue{}
	}
	sp := testSpan("typed", 1)
	sp.Attributes = []*commonpb.KeyValue{
		{Key: "b", Value: anyOf(true)},
		{Key: "i", Value: anyOf(int64(-7))},
		{Key: "d", Value: anyOf(1.5)},
		{Key: "y", Value: anyOf([]byte{1, 2})},
		{Key: "e", Value: anyOf(nil)},
		{Key: "a", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{
			ArrayValue: &commonpb.ArrayValue{Values: []*commonpb.AnyValue{strVal("x"), anyOf(int64(2))}},
		}}},
		{Key: "m", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{
			KvlistValue: &commonpb.KeyValueList{Values: []*commonpb.KeyValue{strKV("k", "v")}},
		}}},
	}
	sp.Events = []*tracepb.Span_Event{{
		Name: "ev", TimeUnixNano: math.MaxUint64, Attributes: []*commonpb.KeyValue{strKV("a", "b")},
	}}
	sp.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "bad"}
	sp.ParentSpanId = idBytes(8, 50)
	sp.Kind = tracepb.Span_SPAN_KIND_CLIENT

	f := newFixture(t, nil)
	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/traces", tracesRequest(sp)))
	require.Equal(t, http.StatusOK, rec.Code)
	spans := f.ing.Spans()
	require.Len(t, spans, 1)
	got := spans[0]
	assert.Equal(t, []trace.Attr{
		{Key: "b", Value: trace.BoolValue(true)},
		{Key: "i", Value: trace.IntValue(-7)},
		{Key: "d", Value: trace.DoubleValue(1.5)},
		{Key: "y", Value: trace.Value{Type: trace.ValueBytes, Bytes: []byte{1, 2}}},
		{Key: "e", Value: trace.Value{}},
		{Key: "a", Value: trace.ArrayValue(trace.StringValue("x"), trace.IntValue(2))},
		{Key: "m", Value: trace.MapValue(trace.Attr{Key: "k", Value: trace.StringValue("v")})},
	}, got.Attributes)
	require.Len(t, got.Events, 1)
	assert.Equal(t, int64(math.MaxInt64), got.Events[0].TimeNs, "timestamps saturate instead of wrapping")
	assert.Equal(t, trace.StatusCodeError, got.StatusCode)
	assert.Equal(t, "bad", got.StatusMessage)
	assert.Equal(t, int32(3), got.SpanKind)
	assert.False(t, got.ParentSpanID.IsZero())
}

func TestConvert_InvalidSpansAreSkipped(t *testing.T) {
	t.Parallel()
	mk := func(mut func(*tracepb.Span)) *tracepb.Span {
		s := testSpan("x", 1)
		mut(s)
		return s
	}
	tests := []struct {
		name string
		span *tracepb.Span
	}{
		{"short trace id", mk(func(s *tracepb.Span) { s.TraceId = s.TraceId[:3] })},
		{"empty trace id", mk(func(s *tracepb.Span) { s.TraceId = nil })},
		{"zero trace id", mk(func(s *tracepb.Span) { s.TraceId = make([]byte, 16) })},
		{"short span id", mk(func(s *tracepb.Span) { s.SpanId = s.SpanId[:3] })},
		{"zero span id", mk(func(s *tracepb.Span) { s.SpanId = make([]byte, 8) })},
		{"bad parent id", mk(func(s *tracepb.Span) { s.ParentSpanId = []byte{1} })},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/traces", tracesRequest(tc.span, testSpan("ok", 2))))
			require.Equal(t, http.StatusOK, rec.Code)
			require.Len(t, f.ing.Spans(), 1)
			assert.Equal(t, "ok", f.ing.Spans()[0].Name)
		})
	}
}

func TestConvert_SpansOfOneResourceShareIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/traces", nSpans(2)))
	require.Equal(t, http.StatusOK, rec.Code)
	spans := f.ing.Spans()
	require.Len(t, spans, 2)
	assert.Same(t, spans[0].Resource, spans[1].Resource)
}
