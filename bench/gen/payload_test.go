// SPDX-License-Identifier: Apache-2.0

package gen_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/thnhdatle/loupe/bench/gen"
)

var plNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func plSpans(req *coltrace.ExportTraceServiceRequest) []*tracepb.Span {
	var out []*tracepb.Span
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			out = append(out, ss.Spans...)
		}
	}
	return out
}

func plAttr(sp *tracepb.Span, key string) string {
	for _, kv := range sp.Attributes {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}

func TestPayloadMix(t *testing.T) {
	t.Parallel()
	p := gen.NewPayload(1, 1)
	req := p.Request(0, plNow)
	require.Len(t, req.ResourceSpans, 1)
	spans := plSpans(req)
	require.Len(t, spans, 512)
	got := map[string]int{}
	for _, sp := range spans {
		got[plAttr(sp, "gen_ai.operation.name")]++
	}
	assert.Equal(t, map[string]int{
		"chat": 205, "execute_tool": 128, "retrieval": 51, "invoke_agent": 51, "invoke_workflow": 77,
	}, got)
}

func TestPayloadCounts(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 10, 100, 512, 1000} {
		c, a, l, to, r := gen.PayloadCounts(n)
		assert.Equal(t, n, c+a+l+to+r)
	}
}

func TestPayloadAttrsAndContent(t *testing.T) {
	t.Parallel()
	p := gen.NewPayload(7, 2)
	var total, spans, llm, content int
	for seq := range uint32(8) {
		for _, sp := range plSpans(p.Request(seq, plNow)) {
			total += gen.PayloadAttrSize(sp)
			spans++
			if plAttr(sp, "gen_ai.operation.name") != "chat" {
				assert.Empty(t, plAttr(sp, gen.PayloadInputKey))
				continue
			}
			llm++
			in, out := plAttr(sp, gen.PayloadInputKey), plAttr(sp, gen.PayloadOutputKey)
			if in != "" {
				content++
				n := len(in) + len(out)
				assert.GreaterOrEqual(t, n, 2<<10)
				assert.LessOrEqual(t, n, 8<<10)
				assert.NotEmpty(t, out)
			}
		}
	}
	mean := float64(total) / float64(spans)
	assert.InDelta(t, 600, mean, 60)
	assert.InDelta(t, 0.10, float64(content)/float64(llm), 0.01)
}

func TestPayloadUniqueAndTree(t *testing.T) {
	t.Parallel()
	p := gen.NewPayload(3, 9)
	seenSpan, seenTrace := map[string]bool{}, map[string]bool{}
	for seq := range uint32(10) {
		req := p.Request(seq, plNow)
		run, got, ok := gen.PayloadSeq(req)
		require.True(t, ok)
		assert.Equal(t, uint32(9), run)
		assert.Equal(t, seq, got)
		ids := map[string]string{}
		traces := map[string]bool{}
		for _, sp := range plSpans(req) {
			assert.False(t, seenSpan[string(sp.SpanId)], "dup span id")
			seenSpan[string(sp.SpanId)] = true
			ids[string(sp.SpanId)] = string(sp.TraceId)
			traces[string(sp.TraceId)] = true
			assert.LessOrEqual(t, sp.EndTimeUnixNano, uint64(plNow.UnixNano()))
			assert.Greater(t, sp.EndTimeUnixNano, sp.StartTimeUnixNano)
		}
		for tid := range traces {
			assert.False(t, seenTrace[tid], "dup trace id")
			seenTrace[tid] = true
		}
		roots := 0
		for _, sp := range plSpans(req) {
			if len(sp.ParentSpanId) == 0 {
				roots++
				continue
			}
			assert.Equal(t, string(sp.TraceId), ids[string(sp.ParentSpanId)], "parent in same trace")
		}
		assert.Equal(t, len(traces), roots)
	}
	assert.Len(t, seenSpan, 10*512)
}

func TestPayloadDeterministic(t *testing.T) {
	t.Parallel()
	a, _ := proto.MarshalOptions{Deterministic: true}.Marshal(gen.NewPayload(5, 1).Request(4, plNow))
	b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(gen.NewPayload(5, 1).Request(4, plNow))
	c, _ := proto.MarshalOptions{Deterministic: true}.Marshal(gen.NewPayload(6, 1).Request(4, plNow))
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
}

func TestPayloadSize(t *testing.T) {
	t.Parallel()
	p := gen.NewPayload(1, 1)
	p.SpansPerRequest = 10
	assert.Len(t, plSpans(p.Request(0, plNow)), 10)
}
