// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func convertValue(v *commonpb.AnyValue) trace.Value {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return trace.StringValue(x.StringValue)
	case *commonpb.AnyValue_BoolValue:
		return trace.BoolValue(x.BoolValue)
	case *commonpb.AnyValue_IntValue:
		return trace.IntValue(x.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return trace.DoubleValue(x.DoubleValue)
	case *commonpb.AnyValue_BytesValue:
		return trace.Value{Type: trace.ValueBytes, Bytes: x.BytesValue}
	case *commonpb.AnyValue_ArrayValue:
		vals := x.ArrayValue.GetValues()
		out := make([]trace.Value, len(vals))
		for i, e := range vals {
			out[i] = convertValue(e)
		}
		return trace.Value{Type: trace.ValueArray, Array: out}
	case *commonpb.AnyValue_KvlistValue:
		return trace.Value{Type: trace.ValueMap, Map: convertAttrs(x.KvlistValue.GetValues())}
	default:
		return trace.Value{}
	}
}

func convertAttrs(kvs []*commonpb.KeyValue) []trace.Attr {
	if len(kvs) == 0 {
		return nil
	}
	out := make([]trace.Attr, len(kvs))
	for i, kv := range kvs {
		out[i] = trace.Attr{Key: kv.GetKey(), Value: convertValue(kv.GetValue())}
	}
	return out
}

func convertResource(r *resourcepb.Resource) *trace.RawResource {
	return &trace.RawResource{Attributes: convertAttrs(r.GetAttributes())}
}

func countSpans(rss []*tracepb.ResourceSpans) int {
	n := 0
	for _, rs := range rss {
		for _, ss := range rs.GetScopeSpans() {
			n += len(ss.GetSpans())
		}
	}
	return n
}

func convertSpans(rss []*tracepb.ResourceSpans) (spans []trace.RawSpan, invalid int) {
	spans = make([]trace.RawSpan, 0, countSpans(rss))
	for _, rs := range rss {
		res := convertResource(rs.GetResource())
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				raw, ok := convertSpan(res, sp)
				if !ok {
					invalid++
					continue
				}
				spans = append(spans, raw)
			}
		}
	}
	return spans, invalid
}

func convertSpan(res *trace.RawResource, sp *tracepb.Span) (trace.RawSpan, bool) {
	tid, err := trace.IDFromBytes(sp.GetTraceId())
	if err != nil || tid.IsZero() {
		return trace.RawSpan{}, false
	}
	sid, err := trace.SpanIDFromBytes(sp.GetSpanId())
	if err != nil || sid.IsZero() {
		return trace.RawSpan{}, false
	}
	parent, err := trace.SpanIDFromBytes(sp.GetParentSpanId())
	if err != nil {
		return trace.RawSpan{}, false
	}
	raw := trace.RawSpan{
		Resource:      res,
		TraceID:       tid,
		SpanID:        sid,
		ParentSpanID:  parent,
		Name:          sp.GetName(),
		SpanKind:      int32(sp.GetKind()),
		StartNs:       clampNs(sp.GetStartTimeUnixNano()),
		EndNs:         clampNs(sp.GetEndTimeUnixNano()),
		StatusCode:    int32(sp.GetStatus().GetCode()),
		StatusMessage: sp.GetStatus().GetMessage(),
		Attributes:    convertAttrs(sp.GetAttributes()),
	}
	if evs := sp.GetEvents(); len(evs) > 0 {
		raw.Events = make([]trace.RawEvent, len(evs))
		for i, e := range evs {
			raw.Events[i] = trace.RawEvent{
				Name:       e.GetName(),
				TimeNs:     clampNs(e.GetTimeUnixNano()),
				Attributes: convertAttrs(e.GetAttributes()),
			}
		}
	}
	return raw, true
}

func clampNs(u uint64) int64 {
	const maxInt64 = 1<<63 - 1
	if u > maxInt64 {
		return maxInt64
	}
	return int64(u)
}

func countLogRecords(rls []*logspb.ResourceLogs) int {
	n := 0
	for _, rl := range rls {
		for _, sl := range rl.GetScopeLogs() {
			n += len(sl.GetLogRecords())
		}
	}
	return n
}

func convertLogs(rls []*logspb.ResourceLogs) []trace.RawLogRecord {
	out := make([]trace.RawLogRecord, 0, countLogRecords(rls))
	for _, rl := range rls {
		res := convertResource(rl.GetResource())
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				out = append(out, convertLog(res, lr))
			}
		}
	}
	return out
}

func convertLog(res *trace.RawResource, lr *logspb.LogRecord) trace.RawLogRecord {
	raw := trace.RawLogRecord{
		Resource:   res,
		TimeNs:     clampNs(lr.GetTimeUnixNano()),
		EventName:  lr.GetEventName(),
		Body:       convertValue(lr.GetBody()),
		Attributes: convertAttrs(lr.GetAttributes()),
	}
	if raw.TimeNs == 0 {
		raw.TimeNs = clampNs(lr.GetObservedTimeUnixNano())
	}
	tid, err := trace.IDFromBytes(lr.GetTraceId())
	if err != nil {
		return raw
	}
	sid, err := trace.SpanIDFromBytes(lr.GetSpanId())
	if err != nil || sid.IsZero() {
		return raw
	}
	raw.TraceID, raw.SpanID = tid, sid
	return raw
}
