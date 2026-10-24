// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"encoding/json"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var (
	testTrace = trace.ID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	testSpan  = trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
)

func sattr(k, v string) trace.Attr { return trace.Attr{Key: k, Value: trace.StringValue(v)} }

func iattr(k string, v int64) trace.Attr { return trace.Attr{Key: k, Value: trace.IntValue(v)} }

func dattr(k string, v float64) trace.Attr { return trace.Attr{Key: k, Value: trace.DoubleValue(v)} }

func rawSpan(attrs ...trace.Attr) *trace.RawSpan {
	return &trace.RawSpan{TraceID: testTrace, SpanID: testSpan, Name: "s", Attributes: attrs}
}

type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func attrsJSON(t fataler, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("attributes are not a JSON object: %v: %s", err, b)
	}
	return m
}
