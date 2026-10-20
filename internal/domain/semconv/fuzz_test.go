// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"encoding/json"
	"testing"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func FuzzTranslate(f *testing.F) {
	f.Add("gen_ai.usage.input_tokens", "12", "input.value", "hello", 8)
	f.Add("llm.input_messages.0.message.role", "user", "llm.input_messages.0.message.content", "é😀", 3)
	f.Add("gen_ai.prompt.x", "\xff\xfe", "gen_ai.completion.1.", "", 0)
	f.Fuzz(func(t *testing.T, k1, v1, k2, v2 string, maxBytes int) {
		raw := rawSpan(sattr(k1, v1), sattr(k2, v2), dattr(k1+"d", 1e300))
		raw.Events = []trace.RawEvent{{Name: k1, Attributes: []trace.Attr{sattr(k2, v2)}}}
		s := semconv.Translate(raw, semconv.Options{MaxAttrBytes: maxBytes % 64})
		if !json.Valid(s.Attributes) {
			t.Fatalf("invalid attributes JSON: %q", s.Attributes)
		}
		rec := &trace.RawLogRecord{
			TraceID: testTrace, SpanID: testSpan, EventName: k1,
			Body: trace.MapValue(sattr(k1, v1)), Attributes: []trace.Attr{sattr(k2, v2)},
		}
		semconv.TranslateLog(rec)
		semconv.UnmappedUsageKeys(raw.Attributes)
	})
}
