//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"encoding/binary"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// LPE-08 workload. One request is lpe08TracesPerRequest agent runs of
// lpe08SpansPerTrace spans each, exported together, plus one GenAI log event
// per run (the openai-v2 gen_ai.choice event of its last model call):
//
//   - 1 invoke_agent root (agent, conversation, user attributes; no content);
//   - 4 chat calls: gen_ai.system_instructions (0.5 KB),
//     gen_ai.input.messages (a growing conversation: 0.6–1 KB, plus 0.3–0.7 KB
//     of answer and 0.3–0.6 KB of follow-up per earlier call) and
//     gen_ai.output.messages (0.3–0.7 KB), with usage, response id, finish
//     reasons and request parameters;
//   - 3 execute_tool calls: gen_ai.tool.call.arguments (0.15–0.3 KB) and
//     gen_ai.tool.call.result (0.4–1.2 KB);
//   - 2 HTTP client spans with the usual http/url/server attributes.
//
// Text is drawn from a Zipf-distributed synthetic vocabulary, which compresses
// with zstd about 2:1 (less than English prose, so the stored size errs high).
// Measured (acceptance/logs/LPE-08.json): raw content per span sent, and at
// the end of day 15 the bytes on disk per stored span and the raw and stored
// content bytes per span with content. Sizes are chosen so that days 1–15 stay
// well below 200 MiB with 7 days of spans and 3 of content, while days 16–30
// would need several times the cap.
const (
	lpe08SpansPerTrace    = 10
	lpe08TracesPerRequest = 5
	lpe08RequestSpans     = lpe08SpansPerTrace * lpe08TracesPerRequest
	lpe08ChatCalls        = 4
	lpe08ToolCalls        = 3
	lpe08Users            = 200
	lpe08TracesPerSession = 8
	lpe08CorpusBytes      = 4 << 20
	lpe08Seed             = 0x1e08
)

// lpe08Gen builds the deterministic workload.
type lpe08Gen struct {
	rnd      *rand.Rand
	corpus   string
	traces   uint64
	spans    uint64
	res      *trace.RawResource
	rawBytes int64 // content bytes generated (attribute values and log bodies)
}

func newLPE08Gen() *lpe08Gen {
	rnd := rand.New(rand.NewPCG(lpe08Seed, lpe08Seed^0x5eed))
	return &lpe08Gen{
		rnd:    rnd,
		corpus: lpe08Corpus(rnd, lpe08CorpusBytes),
		res: &trace.RawResource{Attributes: []trace.Attr{
			lpe08S("service.name", "support-bot"),
			lpe08S("service.version", "2.14.3"),
			lpe08S("deployment.environment.name", "production"),
			lpe08S("telemetry.sdk.name", "opentelemetry"),
			lpe08S("telemetry.sdk.language", "python"),
			lpe08S("telemetry.sdk.version", "1.38.0"),
			lpe08S("host.name", "support-bot-7d9f8c6b5-x2k4q"),
		}},
	}
}

// lpe08Corpus is prose-like text from a Zipf-distributed vocabulary of
// lower-case words, with sentence punctuation, and no characters that need
// JSON escaping.
func lpe08Corpus(rnd *rand.Rand, n int) string {
	const vocab = 6000
	words := make([]string, vocab)
	letters := "etaoinshrdlcumwfgypbvkjxqz"
	for i := range words {
		l := 2 + rnd.IntN(9)
		var b strings.Builder
		for range l {
			// Skewed letter choice, like natural text.
			b.WriteByte(letters[min(rnd.IntN(len(letters)), rnd.IntN(len(letters)))])
		}
		words[i] = b.String()
	}
	z := rand.NewZipf(rnd, 1.1, 2, vocab-1)
	var b strings.Builder
	b.Grow(n + 32)
	for inSentence := 0; b.Len() < n; inSentence++ {
		w := words[z.Uint64()]
		if inSentence == 0 {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		b.WriteString(w)
		switch {
		case inSentence > 6 && rnd.IntN(10) == 0:
			b.WriteString(". ")
			inSentence = -1
		case rnd.IntN(12) == 0:
			b.WriteString(", ")
		default:
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// text returns n bytes of corpus text from a random offset.
func (g *lpe08Gen) text(n int) string {
	off := g.rnd.IntN(len(g.corpus) - n)
	return g.corpus[off : off+n]
}

// textRange returns between lo and hi bytes of text.
func (g *lpe08Gen) textRange(lo, hi int) string { return g.text(lo + g.rnd.IntN(hi-lo+1)) }

func lpe08S(k, v string) trace.Attr { return trace.Attr{Key: k, Value: trace.StringValue(v)} }

func lpe08I(k string, v int64) trace.Attr { return trace.Attr{Key: k, Value: trace.IntValue(v)} }

func lpe08F(k string, v float64) trace.Attr { return trace.Attr{Key: k, Value: trace.DoubleValue(v)} }

// content is a content attribute; its bytes are counted.
func (g *lpe08Gen) content(k, v string) trace.Attr {
	g.rawBytes += int64(len(v))
	return lpe08S(k, v)
}

func (g *lpe08Gen) spanID() trace.SpanID {
	g.spans++
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], g.spans|0x08<<56)
	return id
}

func (g *lpe08Gen) hex(n int) string {
	const digits = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[g.rnd.IntN(len(digits))]
	}
	return string(b)
}

// message is one GenAI message JSON object with a single text part.
func message(role, text string) string {
	return `{"role":"` + role + `","parts":[{"type":"text","content":"` + text + `"}]}`
}

// request returns the spans and log records of one export request whose
// traces start at windowStart and are spread over window.
func (g *lpe08Gen) request(windowStart time.Time, window time.Duration) ([]trace.RawSpan, []trace.RawLogRecord) {
	spans := make([]trace.RawSpan, 0, lpe08RequestSpans)
	logs := make([]trace.RawLogRecord, 0, lpe08TracesPerRequest)
	step := window / lpe08TracesPerRequest
	for i := range lpe08TracesPerRequest {
		start := windowStart.Add(time.Duration(i)*step + time.Duration(g.rnd.Int64N(int64(step/4))))
		// A run lasts at most half its share of the window, so every span
		// starts and ends inside the window.
		s, l := g.agentRun(start, step/2)
		spans = append(spans, s...)
		logs = append(logs, l)
	}
	return spans, logs
}

// agentRun builds one trace of lpe08SpansPerTrace spans within [start, start+maxDur).
func (g *lpe08Gen) agentRun(start time.Time, maxDur time.Duration) ([]trace.RawSpan, trace.RawLogRecord) {
	g.traces++
	var tid trace.ID
	binary.BigEndian.PutUint64(tid[:8], 0x0808_0000_0000_0000|g.traces)
	binary.BigEndian.PutUint64(tid[8:], g.rnd.Uint64())
	session := "conv-" + strconv.FormatUint(g.traces/lpe08TracesPerSession, 10)
	user := "user-" + strconv.Itoa(int((g.traces/lpe08TracesPerSession)%lpe08Users))
	slot := maxDur / (lpe08SpansPerTrace + 1)
	root := g.spanID()
	spans := make([]trace.RawSpan, 0, lpe08SpansPerTrace)
	spans = append(spans, trace.RawSpan{
		Resource: g.res, TraceID: tid, SpanID: root, Name: "invoke_agent support-agent", SpanKind: 1,
		StartNs: start.UnixNano(), EndNs: start.Add(maxDur - slot/2).UnixNano(), StatusCode: trace.StatusCodeOK,
		Attributes: []trace.Attr{
			lpe08S("gen_ai.operation.name", "invoke_agent"),
			lpe08S("gen_ai.agent.name", "support-agent"),
			lpe08S("gen_ai.agent.id", "asst_"+g.hex(24)),
			lpe08S("gen_ai.conversation.id", session),
			lpe08S("user.id", user),
		},
	})
	at := func(i int) (int64, int64) {
		s := start.Add(time.Duration(i) * slot)
		return s.UnixNano(), s.Add(slot * 9 / 10).UnixNano()
	}
	system := g.content("gen_ai.system_instructions", `[{"type":"text","content":"`+g.text(500)+`"}]`)
	history := make([]string, 0, 1+2*lpe08ChatCalls)
	history = append(history, message("user", g.textRange(600, 1000)))
	var choice trace.RawLogRecord
	for c := range lpe08ChatCalls {
		s, e := at(1 + 2*c)
		answer := g.textRange(300, 700)
		spans = append(spans, g.chat(lpe08Call{
			tid: tid, parent: root, start: s, end: e, session: session, user: user,
			system: system, history: history, answer: answer,
		}))
		history = append(history, message("assistant", answer), message("user", g.textRange(300, 600)))
		if c == lpe08ChatCalls-1 {
			choice = g.choiceEvent(tid, spans[len(spans)-1].SpanID, e, answer)
		}
		if c < lpe08ToolCalls {
			s, e = at(2 + 2*c)
			spans = append(spans, g.tool(tid, root, s, e))
		}
	}
	for h := range 2 {
		s, e := at(8 + h)
		spans = append(spans, g.http(tid, root, s, e))
	}
	return spans, choice
}

// lpe08Call describes one chat call of an agent run.
type lpe08Call struct {
	tid           trace.ID
	parent        trace.SpanID
	start, end    int64
	session, user string
	system        trace.Attr
	history       []string
	answer        string
}

func (g *lpe08Gen) chat(c lpe08Call) trace.RawSpan {
	system, history, answer, session, user := c.system, c.history, c.answer, c.session, c.user
	in := int64(len(system.Value.Str)/4) + int64(len(strings.Join(history, ""))/4)
	return trace.RawSpan{
		Resource: g.res, TraceID: c.tid, SpanID: g.spanID(), ParentSpanID: c.parent, Name: "chat gpt-4o-mini",
		SpanKind: 3, StartNs: c.start, EndNs: c.end, StatusCode: trace.StatusCodeOK,
		Attributes: []trace.Attr{
			lpe08S("gen_ai.operation.name", "chat"),
			lpe08S("gen_ai.provider.name", "openai"),
			lpe08S("gen_ai.request.model", "gpt-4o-mini"),
			lpe08S("gen_ai.response.model", "gpt-4o-mini-2024-07-18"),
			lpe08S("gen_ai.response.id", "chatcmpl-"+g.hex(29)),
			{Key: "gen_ai.response.finish_reasons", Value: trace.ArrayValue(trace.StringValue("stop"))},
			lpe08F("gen_ai.request.temperature", 0.2),
			lpe08I("gen_ai.request.max_tokens", 1024),
			lpe08I("gen_ai.usage.input_tokens", in),
			lpe08I("gen_ai.usage.output_tokens", int64(len(answer)/4)),
			lpe08S("gen_ai.conversation.id", session),
			lpe08S("user.id", user),
			lpe08S("server.address", "api.openai.com"),
			lpe08I("server.port", 443),
			system,
			g.content("gen_ai.input.messages", "["+strings.Join(history, ",")+"]"),
			g.content("gen_ai.output.messages",
				`[{"role":"assistant","parts":[{"type":"text","content":"`+answer+`"}],"finish_reason":"stop"}]`),
		},
	}
}

func (g *lpe08Gen) tool(tid trace.ID, parent trace.SpanID, s, e int64) trace.RawSpan {
	return trace.RawSpan{
		Resource: g.res, TraceID: tid, SpanID: g.spanID(), ParentSpanID: parent, Name: "execute_tool search_kb",
		StartNs: s, EndNs: e, StatusCode: trace.StatusCodeOK,
		Attributes: []trace.Attr{
			lpe08S("gen_ai.operation.name", "execute_tool"),
			lpe08S("gen_ai.tool.name", "search_kb"),
			lpe08S("gen_ai.tool.type", "function"),
			lpe08S("gen_ai.tool.call.id", "call_"+g.hex(24)),
			g.content("gen_ai.tool.call.arguments", `{"query":"`+g.textRange(150, 300)+`","top_k":5}`),
			g.content("gen_ai.tool.call.result", `{"documents":["`+g.textRange(400, 1200)+`"]}`),
		},
	}
}

func (g *lpe08Gen) http(tid trace.ID, parent trace.SpanID, s, e int64) trace.RawSpan {
	id := strconv.Itoa(100000 + g.rnd.IntN(900000))
	return trace.RawSpan{
		Resource: g.res, TraceID: tid, SpanID: g.spanID(), ParentSpanID: parent, Name: "GET",
		SpanKind: 3, StartNs: s, EndNs: e, StatusCode: trace.StatusCodeUnset,
		Attributes: []trace.Attr{
			lpe08S("http.request.method", "GET"),
			lpe08S("url.full", "https://orders.internal.example/api/v2/orders/"+id),
			lpe08S("url.template", "/api/v2/orders/{id}"),
			lpe08I("http.response.status_code", 200),
			lpe08S("server.address", "orders.internal.example"),
			lpe08I("server.port", 443),
			lpe08S("network.protocol.version", "1.1"),
		},
	}
}

// choiceEvent is the openai-v2 gen_ai.choice log event of a chat span.
func (g *lpe08Gen) choiceEvent(tid trace.ID, sid trace.SpanID, t int64, answer string) trace.RawLogRecord {
	g.rawBytes += int64(len(answer))
	return trace.RawLogRecord{
		Resource: g.res, TraceID: tid, SpanID: sid, TimeNs: t, EventName: "gen_ai.choice",
		Body: trace.MapValue(
			trace.Attr{Key: "index", Value: trace.IntValue(0)},
			trace.Attr{Key: "finish_reason", Value: trace.StringValue("stop")},
			trace.Attr{Key: "message", Value: trace.MapValue(
				trace.Attr{Key: "role", Value: trace.StringValue("assistant")},
				trace.Attr{Key: "content", Value: trace.StringValue(answer)},
			)},
		),
		Attributes: []trace.Attr{lpe08S("gen_ai.system", "openai")},
	}
}
