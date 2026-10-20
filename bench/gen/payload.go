// SPDX-License-Identifier: Apache-2.0

package gen

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Defaults of the LPE-02 payload.
const (
	// PayloadSpans is the number of spans in one LPE-02 request.
	PayloadSpans = 512
	// PayloadAttrBytes is the mean JSON size of the non-content attributes of a span.
	PayloadAttrBytes = 600
	// payloadContentFraction is the share of llm spans that carry content attributes.
	payloadContentFraction = 0.10
	plContentMin           = 2 << 10
	plContentMax           = 8 << 10
)

// Content attribute keys; they are excluded from the attribute size measure.
const (
	PayloadInputKey  = "gen_ai.input.messages"
	PayloadOutputKey = "gen_ai.output.messages"
)

type plKind int

const (
	plChain plKind = iota
	plAgent
	plLLM
	plTool
	plRetrieval
)

// plKinds is the number of span kinds.
const plKinds = 5

const (
	plOpenAI    = "openai"
	plAnthropic = "anthropic"
)

// plShare is the span mix, in the order of the plKind constants.
var plShare = [plKinds]float64{plChain: 0.15, plAgent: 0.10, plLLM: 0.40, plTool: 0.25, plRetrieval: 0.10}

var (
	plServices = []string{"support-bot", "research-agent", "code-review", "doc-qa", "order-assistant"}
	plModels   = []struct{ provider, model string }{
		{plOpenAI, "gpt-4o"},
		{plOpenAI, "gpt-4o-mini"},
		{plAnthropic, "claude-sonnet-4-5"},
		{plAnthropic, "claude-haiku-4-5"},
		{"google", "gemini-2.5-flash"},
	}
	plTools = []string{"web_search", "read_file", "run_sql", "send_email", "get_weather", "calc"}
	plWords = strings.Fields("the quick agent model response tool call result data user request " +
		"summary context document search order status invoice policy answer question token " +
		"customer ticket update system plan step reason final value record")
)

// Payload generates deterministic LPE-02 export requests. Request(seq) depends only
// on the seed, the run id, seq and now, so it is safe for concurrent use.
type Payload struct {
	seed  uint64
	runID uint32
	// SpansPerRequest is the number of spans per request (default PayloadSpans).
	SpansPerRequest int
}

// NewPayload returns a generator for one run. Span and trace ids are unique across
// the requests of a run and carry runID and the request sequence number.
func NewPayload(seed uint64, runID uint32) *Payload {
	return &Payload{seed: seed, runID: runID, SpansPerRequest: PayloadSpans}
}

// PayloadCounts splits n spans over the kinds in the order chain, agent, llm,
// tool, retrieval by the largest-remainder method.
func PayloadCounts(n int) (chain, agent, llm, tool, retrieval int) {
	var c [plKinds]int
	var rem [plKinds]float64
	sum := 0
	for k := range plKinds {
		x := plShare[k] * float64(n)
		c[k] = int(math.Floor(x + 1e-9))
		rem[k] = x - float64(c[k])
		sum += c[k]
	}
	for ; sum < n; sum++ {
		best := 0
		for k := 1; k < plKinds; k++ {
			if rem[k] > rem[best]+1e-12 {
				best = k
			}
		}
		c[best]++
		rem[best] = -1
	}
	return c[plChain], c[plAgent], c[plLLM], c[plTool], c[plRetrieval]
}

// PayloadSeq reports the run id and request sequence number carried by the trace ids
// of a request produced by Request.
func PayloadSeq(req *coltrace.ExportTraceServiceRequest) (runID, seq uint32, ok bool) {
	rs := req.GetResourceSpans()
	if len(rs) == 0 || len(rs[0].GetScopeSpans()) == 0 || len(rs[0].ScopeSpans[0].GetSpans()) == 0 {
		return 0, 0, false
	}
	id := rs[0].ScopeSpans[0].Spans[0].TraceId
	if len(id) != 16 || id[0] != 0x4c || id[1] != 0x02 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint32(id[2:]), binary.BigEndian.Uint32(id[6:]), true
}

// Request returns request number seq with all spans ending at or before now.
func (p *Payload) Request(seq uint32, now time.Time) *coltrace.ExportTraceServiceRequest {
	n := p.SpansPerRequest
	if n <= 0 {
		n = PayloadSpans
	}
	r := rand.New(rand.NewPCG(p.seed, uint64(p.runID)<<32|uint64(seq))) //nolint:gosec // deterministic benchmark data.
	kinds := plShuffledKinds(n, r)
	withContent := plContentSet(kinds, r)

	spans := make([]*tracepb.Span, 0, n)
	var tidx uint16
	for off := 0; off < n; {
		size := min(4+r.IntN(17), n-off)
		group := make([]int, size) // indexes into kinds, containers first
		for i := range group {
			group[i] = off + i
		}
		plContainersFirst(group, kinds)
		tidx++
		tid := p.traceID(seq, tidx, r)
		var containers [][]byte
		for gi, ki := range group {
			sid := p.spanID(seq, len(spans)+1)
			sp := &tracepb.Span{TraceId: tid, SpanId: sid}
			if gi > 0 {
				if len(containers) == 0 {
					sp.ParentSpanId = spans[len(spans)-gi].SpanId
				} else {
					sp.ParentSpanId = containers[r.IntN(len(containers))]
				}
			}
			p.fillSpan(sp, kinds[ki], withContent[ki], now, r)
			if kinds[ki] == plChain || kinds[ki] == plAgent {
				containers = append(containers, sid)
			}
			spans = append(spans, sp)
		}
		off += size
	}
	return &coltrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: plResource(r),
		ScopeSpans: []*tracepb.ScopeSpans{{
			Scope: &commonpb.InstrumentationScope{Name: "loupe.bench", Version: "1"},
			Spans: spans,
		}},
	}}}
}

// plShuffledKinds returns n span kinds in the exact mix, shuffled.
func plShuffledKinds(n int, r *rand.Rand) []plKind {
	chain, agent, llm, tool, retr := PayloadCounts(n)
	kinds := make([]plKind, 0, n)
	for k, c := range [plKinds]int{plChain: chain, plAgent: agent, plLLM: llm, plTool: tool, plRetrieval: retr} {
		for range c {
			kinds = append(kinds, plKind(k))
		}
	}
	r.Shuffle(len(kinds), func(i, j int) { kinds[i], kinds[j] = kinds[j], kinds[i] })
	return kinds
}

// plContentSet picks exactly round(10% of llm) llm spans to carry content.
func plContentSet(kinds []plKind, r *rand.Rand) map[int]bool {
	var llmIdx []int
	for i, k := range kinds {
		if k == plLLM {
			llmIdx = append(llmIdx, i)
		}
	}
	r.Shuffle(len(llmIdx), func(i, j int) { llmIdx[i], llmIdx[j] = llmIdx[j], llmIdx[i] })
	set := map[int]bool{}
	for _, i := range llmIdx[:int(math.Round(payloadContentFraction*float64(len(llmIdx))))] {
		set[i] = true
	}
	return set
}

func plResource(r *rand.Rand) *resourcepb.Resource {
	svc := plServices[r.IntN(len(plServices))]
	return &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
		plStr("service.name", svc),
		plStr("service.version", fmt.Sprintf("1.%d.%d", r.IntN(10), r.IntN(20))),
		plStr("deployment.environment.name", "production"),
		plStr("host.name", fmt.Sprintf("%s-%02d", svc, r.IntN(12))),
		plStr("telemetry.sdk.name", "opentelemetry"),
		plStr("telemetry.sdk.language", "python"),
		plStr("telemetry.sdk.version", "1.38.0"),
	}}
}

// plContainersFirst stably moves chain and agent spans to the front of group.
func plContainersFirst(group []int, kinds []plKind) {
	out := make([]int, 0, len(group))
	for _, i := range group {
		if kinds[i] == plChain || kinds[i] == plAgent {
			out = append(out, i)
		}
	}
	for _, i := range group {
		if kinds[i] != plChain && kinds[i] != plAgent {
			out = append(out, i)
		}
	}
	copy(group, out)
}

// traceID layout: 0x4c 0x02, runID, seq, trace index in request, 4 random bytes.
func (p *Payload) traceID(seq uint32, idx uint16, r *rand.Rand) []byte {
	b := make([]byte, 16)
	b[0], b[1] = 0x4c, 0x02
	binary.BigEndian.PutUint32(b[2:], p.runID)
	binary.BigEndian.PutUint32(b[6:], seq)
	binary.BigEndian.PutUint16(b[10:], idx)
	binary.BigEndian.PutUint32(b[12:], r.Uint32())
	return b
}

// spanID layout: runID (low 16 bits), seq, span index in request (1-based).
func (p *Payload) spanID(seq uint32, idx int) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint16(b, uint16(p.runID)) //nolint:gosec // low 16 bits by design.
	binary.BigEndian.PutUint32(b[2:], seq)
	binary.BigEndian.PutUint16(b[6:], uint16(idx)) //nolint:gosec // at most 65535 spans per request.
	return b
}

func plStr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func plInt(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

func plDbl(k string, v float64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v}}}
}

func plLognormal(r *rand.Rand, median, sigma float64) float64 {
	return median * math.Exp(sigma*r.NormFloat64())
}

func plHex(r *rand.Rand, n int) string {
	const digits = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[r.IntN(16)]
	}
	return string(b)
}

func (p *Payload) fillSpan(sp *tracepb.Span, kind plKind, content bool, now time.Time, r *rand.Rand) {
	dur := time.Duration(math.Min(plLognormal(r, 300, 0.8), 20000) * float64(time.Millisecond))
	end := now.Add(-time.Duration(r.IntN(2000)) * time.Millisecond)
	sp.StartTimeUnixNano = uint64(end.Add(-dur).UnixNano())
	sp.EndTimeUnixNano = uint64(end.UnixNano())
	sp.Kind = tracepb.Span_SPAN_KIND_INTERNAL

	session := fmt.Sprintf("sess-%d-%s", p.runID, plHex(r, 6))
	var attrs []*commonpb.KeyValue
	switch kind {
	case plLLM:
		m := plModels[r.IntN(len(plModels))]
		sp.Name, sp.Kind = "chat "+m.model, tracepb.Span_SPAN_KIND_CLIENT
		attrs = []*commonpb.KeyValue{
			plStr("gen_ai.operation.name", "chat"),
			plStr("gen_ai.provider.name", m.provider),
			plStr("gen_ai.request.model", m.model),
			plStr("gen_ai.response.model", m.model+"-2025"),
			plStr("gen_ai.response.id", "resp_"+plHex(r, 24)),
			plInt("gen_ai.usage.input_tokens", int64(plLognormal(r, 800, 0.9))+1),
			plInt("gen_ai.usage.output_tokens", int64(plLognormal(r, 250, 0.9))+1),
			plDbl("gen_ai.request.temperature", float64(r.IntN(11))/10),
			plInt("gen_ai.request.max_tokens", 1024<<r.IntN(3)),
			plStr("gen_ai.response.finish_reasons", "stop"),
		}
	case plTool:
		t := plTools[r.IntN(len(plTools))]
		sp.Name = "execute_tool " + t
		attrs = []*commonpb.KeyValue{
			plStr("gen_ai.operation.name", "execute_tool"),
			plStr("gen_ai.tool.name", t),
			plStr("gen_ai.tool.type", "function"),
			plStr("gen_ai.tool.call.id", "call_"+plHex(r, 24)),
		}
	case plRetrieval:
		sp.Name, sp.Kind = "retrieval kb", tracepb.Span_SPAN_KIND_CLIENT
		attrs = []*commonpb.KeyValue{
			plStr("gen_ai.operation.name", "retrieval"),
			plStr("gen_ai.data_source.id", "kb-"+plHex(r, 4)),
			plInt("gen_ai.request.top_k", int64(3+r.IntN(8))),
		}
	case plAgent:
		sp.Name = "invoke_agent planner"
		attrs = []*commonpb.KeyValue{
			plStr("gen_ai.operation.name", "invoke_agent"),
			plStr("gen_ai.agent.name", "planner"),
			plStr("gen_ai.agent.id", "agent-"+plHex(r, 8)),
		}
	case plChain:
		sp.Name = "invoke_workflow pipeline"
		attrs = []*commonpb.KeyValue{
			plStr("gen_ai.operation.name", "invoke_workflow"),
			plStr("gen_ai.workflow.name", "pipeline"),
		}
	}
	attrs = append(attrs,
		plStr("session.id", session),
		plStr("user.id", fmt.Sprintf("user-%d", r.IntN(500))),
		plStr("deployment.region", "eu-west-"+strconv.Itoa(1+r.IntN(3))),
	)
	// The target is uniform in [540, 660], so the mean is PayloadAttrBytes.
	target := PayloadAttrBytes - 60 + r.IntN(121)
	attrs = plPad(attrs, target, r)
	if content {
		total := plContentMin + r.IntN(plContentMax-plContentMin+1)
		in := total * 6 / 10
		attrs = append(attrs,
			plStr(PayloadInputKey, plMessages("user", in, r)),
			plStr(PayloadOutputKey, plMessages("assistant", total-in, r)))
	}
	sp.Attributes = attrs
}

// PayloadAttrSize returns the JSON size of the key to value map of the non-content
// attributes of a span.
func PayloadAttrSize(sp *tracepb.Span) int {
	m := make(map[string]any, len(sp.Attributes))
	for _, kv := range sp.Attributes {
		if kv.Key == PayloadInputKey || kv.Key == PayloadOutputKey {
			continue
		}
		switch v := kv.Value.Value.(type) {
		case *commonpb.AnyValue_StringValue:
			m[kv.Key] = v.StringValue
		case *commonpb.AnyValue_IntValue:
			m[kv.Key] = v.IntValue
		case *commonpb.AnyValue_DoubleValue:
			m[kv.Key] = v.DoubleValue
		}
	}
	b, _ := json.Marshal(m)
	return len(b)
}

// plPad appends hex-valued attributes until the JSON size is target bytes (or the
// base already exceeds it).
func plPad(attrs []*commonpb.KeyValue, target int, r *rand.Rand) []*commonpb.KeyValue {
	sp := &tracepb.Span{Attributes: attrs}
	for i := 0; ; i++ {
		need := target - PayloadAttrSize(sp)
		key := fmt.Sprintf("app.meta.%02d", i)
		// Each added attribute costs len(key)+6 bytes (quotes, colon, comma, quotes).
		overhead := len(key) + 6
		if need <= overhead {
			return sp.Attributes
		}
		l := need - overhead
		if l > 64 {
			l = 64
			if need-(overhead+l) < overhead+1 { // avoid a remainder too small to fill
				l = need - overhead - overhead - 1
			}
		}
		sp.Attributes = append(sp.Attributes, plStr(key, plHex(r, l)))
	}
}

// plMessages returns a gen_ai messages JSON document of exactly size bytes.
func plMessages(role string, size int, r *rand.Rand) string {
	prefix := `[{"role":"` + role + `","parts":[{"type":"text","content":"`
	const suffix = `"}]}]`
	var sb strings.Builder
	sb.Grow(size)
	sb.WriteString(prefix)
	for sb.Len() < size-len(suffix) {
		w := plWords[r.IntN(len(plWords))]
		if left := size - len(suffix) - sb.Len(); len(w)+1 > left {
			sb.WriteString(strings.Repeat("x", left))
			break
		}
		sb.WriteString(w)
		sb.WriteByte(' ')
	}
	sb.WriteString(suffix)
	return sb.String()
}
