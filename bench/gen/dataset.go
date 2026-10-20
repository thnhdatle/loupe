// SPDX-License-Identifier: Apache-2.0

package gen

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Dataset shape constants (LPE-04).
const dsOpenAI = "openai"

const (
	dsServices     = 20
	dsModelPool    = 50
	dsUsers        = 500
	dsMinSpans     = 3
	dsMaxSpans     = 40
	dsMeanSpans    = 20
	dsMaxSession   = 200
	dsMaxDepth     = 4
	dsMax40Traces  = 60
	dsMaxLongTrace = 10 * time.Minute
)

// DatasetConfig selects the size and anchor of a Dataset.
type DatasetConfig struct {
	Spans int       // total spans, hit exactly
	Days  int       // time range length in days
	End   time.Time // exclusive end of the range (the dataset anchor)
	Seed  uint64
}

// Trace is one generated agent run.
type Trace struct {
	Index   int
	ID      trace.ID
	Session string
	User    string
	Service string
	Spans   []trace.RawSpan
}

// Manifest describes a dataset so that benchmarks can rotate parameters.
type Manifest struct {
	Seed          uint64              `json:"seed"`
	Spans         int                 `json:"spans"`
	Traces        int                 `json:"traces"`
	Sessions      int                 `json:"sessions"`
	End           string              `json:"end"`
	Days          int                 `json:"days"`
	Services      []string            `json:"services"`
	Models        []string            `json:"models"`
	ServiceModels map[string][]string `json:"service_models"`
	Session200    string              `json:"session_200"`
	Sessions200   []string            `json:"sessions_200"`
	Traces40      []string            `json:"traces_40"`
}

// Dataset is a deterministic synthetic dataset. Trace(i) depends only on the
// config and i, so traces may be generated concurrently and in any order.
type Dataset struct {
	cfg        DatasetConfig
	counts     []int   // spans per trace
	sessionOf  []int   // session index per trace
	sessionSvc []int   // service per session
	sessionUsr []int   // user per session
	sessSize   []int   // traces per session
	svcModels  [][]int // model indexes per service
	models     []dsModel
	resources  []*trace.RawResource
	traces40   []int
}

type dsModel struct{ name, provider string }

// NewDataset plans the dataset: trace sizes, sessions and the service/model map.
func NewDataset(cfg DatasetConfig) (*Dataset, error) {
	if cfg.Days <= 0 {
		return nil, errors.New("gen: days must be positive")
	}
	if cfg.Spans < dsMinSpans {
		return nil, fmt.Errorf("gen: spans must be at least %d", dsMinSpans)
	}
	d := &Dataset{cfg: cfg, models: dsModels()}
	rng := rand.New(rand.NewPCG(cfg.Seed, 0xd47a5e7)) //nolint:gosec // deterministic benchmark data.
	n := max(1, cfg.Spans/dsMeanSpans)
	for n*dsMinSpans > cfg.Spans {
		n--
	}
	d.planCounts(rng, n)
	d.planSessions(rng, n)
	d.planServices(rng)
	return d, nil
}

func dsModels() []dsModel {
	ms := []dsModel{
		{"gpt-4o", dsOpenAI},
		{"gpt-4o-mini", dsOpenAI},
		{"o3-mini", dsOpenAI},
		{"gpt-4.1", dsOpenAI},
		{"claude-sonnet-4", "anthropic"},
		{"claude-3-5-haiku-20241022", "anthropic"},
		{"gemini-2.5-flash", "gcp.gemini"},
		{"llama-3.1-8b-instant", "groq"},
		{"mistral-large-2411", "mistral_ai"},
		{"deepseek-reasoner", "deepseek"},
	}
	for i := 0; len(ms) < dsModelPool; i++ {
		ms = append(ms, dsModel{fmt.Sprintf("bench-model-%02d", i), "bench"})
	}
	return ms
}

// planCounts fixes the span count of every trace; they sum to cfg.Spans.
func (d *Dataset) planCounts(rng *rand.Rand, n int) {
	k := min(dsMax40Traces, n/10)
	for k > 0 && k*dsMaxSpans+(n-k)*dsMinSpans > d.cfg.Spans {
		k--
	}
	d.counts = make([]int, n)
	fixed := make([]bool, n)
	for _, i := range rng.Perm(n)[:k] {
		d.counts[i], fixed[i] = dsMaxSpans, true
		d.traces40 = append(d.traces40, i)
	}
	sort.Ints(d.traces40)
	sum := k * dsMaxSpans
	for i := range d.counts {
		if !fixed[i] {
			// Uniform 3..37 has mean 20.
			d.counts[i] = dsMinSpans + rng.IntN(35)
			sum += d.counts[i]
		}
	}
	free := n - k
	for diff := d.cfg.Spans - sum; diff != 0 && free > 0; diff = d.cfg.Spans - sum {
		i := rng.IntN(n)
		if fixed[i] {
			continue
		}
		step := min(abs(diff), 1+rng.IntN(8))
		if diff < 0 {
			step = -min(step, d.counts[i]-dsMinSpans)
		} else {
			step = min(step, dsMaxSpans-1-d.counts[i])
		}
		d.counts[i] += step
		sum += step
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (d *Dataset) planSessions(rng *rand.Rand, n int) {
	var sizes []int
	left := n
	if n >= dsMaxSession {
		// One session of exactly 200 per 25k traces, at least one.
		for range max(1, n/25_000) {
			sizes = append(sizes, dsMaxSession)
			left -= dsMaxSession
		}
	}
	for left > 0 {
		s := min(left, 1+rng.IntN(60))
		sizes = append(sizes, s)
		left -= s
	}
	d.sessSize = sizes
	d.sessionOf = make([]int, 0, n)
	for s, sz := range sizes {
		for range sz {
			d.sessionOf = append(d.sessionOf, s)
		}
	}
	rng.Shuffle(len(d.sessionOf), func(i, j int) { d.sessionOf[i], d.sessionOf[j] = d.sessionOf[j], d.sessionOf[i] })
	d.sessionSvc = make([]int, len(sizes))
	d.sessionUsr = make([]int, len(sizes))
	for s := range sizes {
		d.sessionSvc[s] = rng.IntN(dsServices)
		d.sessionUsr[s] = rng.IntN(dsUsers)
	}
}

func (d *Dataset) planServices(rng *rand.Rand) {
	d.svcModels = make([][]int, dsServices)
	for m := range d.models { // every model is used by at least one service
		d.svcModels[m%dsServices] = append(d.svcModels[m%dsServices], m)
	}
	for s := range d.svcModels {
		want := 2 + rng.IntN(4)
		for len(d.svcModels[s]) < want {
			m := rng.IntN(len(d.models))
			if !containsInt(d.svcModels[s], m) {
				d.svcModels[s] = append(d.svcModels[s], m)
			}
		}
	}
	d.resources = make([]*trace.RawResource, dsServices)
	for s := range d.resources {
		d.resources[s] = &trace.RawResource{Attributes: []trace.Attr{
			dsStr("service.name", dsServiceName(s)),
			dsStr("service.version", fmt.Sprintf("1.%d.0", s%7)),
			dsStr("deployment.environment", "production"),
			dsStr("telemetry.sdk.language", "python"),
		}}
	}
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func dsServiceName(s int) string { return fmt.Sprintf("svc-%02d", s) }

// NumTraces returns the number of traces.
func (d *Dataset) NumTraces() int { return len(d.counts) }

// NumSpans returns the total number of spans.
func (d *Dataset) NumSpans() int { return d.cfg.Spans }

// SpanCount returns the number of spans of trace i.
func (d *Dataset) SpanCount(i int) int { return d.counts[i] }

// All yields every trace in index order.
func (d *Dataset) All() iter.Seq[Trace] {
	return func(yield func(Trace) bool) {
		for i := range d.counts {
			if !yield(d.Trace(i)) {
				return
			}
		}
	}
}

func dsMix(a, b, c uint64) uint64 {
	x := a*0x9e3779b97f4a7c15 ^ (b+0x7f4a7c15)*0xbf58476d1ce4e5b9 ^ (c+1)*0x94d049bb133111eb
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ x>>31
}

func (d *Dataset) traceID(i int) trace.ID {
	var id trace.ID
	binary.BigEndian.PutUint64(id[:8], dsMix(d.cfg.Seed, uint64(i), 1)|1) //nolint:gosec // i >= 0.
	binary.BigEndian.PutUint64(id[8:], dsMix(d.cfg.Seed, uint64(i), 2))   //nolint:gosec // i >= 0.
	return id
}

// Manifest returns the dataset description. It is cheap: no trace is built.
func (d *Dataset) Manifest() Manifest {
	m := Manifest{
		Seed: d.cfg.Seed, Spans: d.cfg.Spans, Traces: len(d.counts), Sessions: len(d.sessSize),
		End: d.cfg.End.UTC().Format(time.RFC3339), Days: d.cfg.Days,
		ServiceModels: map[string][]string{},
		Traces40:      []string{},
		Sessions200:   []string{},
	}
	for s := range dsServices {
		name := dsServiceName(s)
		m.Services = append(m.Services, name)
		for _, mi := range d.svcModels[s] {
			m.ServiceModels[name] = append(m.ServiceModels[name], d.models[mi].name)
		}
		sort.Strings(m.ServiceModels[name])
	}
	for _, mo := range d.models {
		m.Models = append(m.Models, mo.name)
	}
	for s, sz := range d.sessSize {
		if sz == dsMaxSession {
			m.Sessions200 = append(m.Sessions200, dsSessionID(s))
		}
	}
	if len(m.Sessions200) > 0 {
		m.Session200 = m.Sessions200[0]
	}
	for _, i := range d.traces40 {
		m.Traces40 = append(m.Traces40, hex.EncodeToString(func() []byte { id := d.traceID(i); return id[:] }()))
	}
	return m
}

func dsSessionID(s int) string { return fmt.Sprintf("sess-%05d", s) }

// Span kinds of the generator, as the operation they carry.
const (
	dsAgent = iota
	dsLLM
	dsTool
	dsRetrieval
	dsChain
)

var (
	dsAgentNames = []string{"planner", "researcher", "coder", "support", "router", "reviewer"}
	dsToolNames  = []string{"web_search", "calculator", "sql_query", "send_email", "read_file", "http_get", "code_exec"}
	dsChainNames = []string{"summarize", "extract", "classify", "rewrite", "validate"}
	dsRoutes     = []string{"/v1/search", "/v1/documents", "/v1/orders", "/v1/users", "/v1/tickets"}
	dsWords      = strings.Fields(`the of and to in is that for with as on are by this from or be at an it not
		data model request result user system answer query order status report plan step tool value
		time first next check update search document record customer price total error review summary
		context policy account response agent task input output select compare confirm provide`)
)

type dsSpan struct {
	kind       int
	depth      int
	start, end int64
}

// Trace builds trace i.
func (d *Dataset) Trace(i int) Trace {
	rng := rand.New(rand.NewPCG(d.cfg.Seed, uint64(i)+1)) //nolint:gosec // deterministic benchmark data; i >= 0.
	n := d.counts[i]
	sess := d.sessionOf[i]
	svc := d.sessionSvc[sess]
	t := Trace{
		Index: i, ID: d.traceID(i), Session: dsSessionID(sess),
		User: fmt.Sprintf("user-%03d", d.sessionUsr[sess]), Service: dsServiceName(svc),
		Spans: make([]trace.RawSpan, n),
	}
	rangeNs := int64(d.cfg.Days) * int64(24*time.Hour)
	startNs := d.cfg.End.UnixNano() - rangeNs
	rootDur := int64(dsLogNormal(rng, float64(40*time.Second), 0.6))
	rootDur = min(max(rootDur, int64(5*time.Second)), int64(dsMaxLongTrace))
	t0 := startNs + rng.Int64N(rangeNs-rootDur)
	models := d.svcModels[svc]

	info := make([]dsSpan, n)
	spanIDs := make([]trace.SpanID, n)
	for j := range spanIDs {
		binary.BigEndian.PutUint64(spanIDs[j][:], dsMix(d.cfg.Seed, uint64(i), uint64(j)+16)|1) //nolint:gosec // j >= 0.
	}
	var parents []int // eligible parents: agent and chain spans below max depth
	for j := range n {
		sp := &t.Spans[j]
		sp.Resource, sp.TraceID, sp.SpanID = d.resources[svc], t.ID, spanIDs[j]
		var parent int
		if j == 0 {
			info[0] = dsSpan{kind: dsAgent, start: t0, end: t0 + rootDur}
		} else {
			parent = parents[rng.IntN(len(parents))]
			p := info[parent]
			kind := dsPickKind(rng)
			st := p.start + int64(rng.Float64()*0.7*float64(p.end-p.start))
			var dur int64
			switch kind {
			case dsLLM:
				dur = int64(dsLogNormal(rng, 1.5e9, 1.0))
			case dsAgent, dsChain:
				dur = int64(dsLogNormal(rng, 6e9, 0.8))
			case dsRetrieval:
				dur = int64(dsLogNormal(rng, 3e8, 0.7))
			default:
				dur = int64(dsLogNormal(rng, 5e8, 1.0))
			}
			dur = max(min(dur, p.end-st), int64(time.Millisecond))
			info[j] = dsSpan{kind: kind, depth: p.depth + 1, start: st, end: st + dur}
			sp.ParentSpanID = spanIDs[parent]
		}
		if k := info[j].kind; (k == dsAgent || k == dsChain) && info[j].depth < dsMaxDepth {
			parents = append(parents, j)
		}
		sp.StartNs, sp.EndNs = info[j].start, info[j].end
		d.fill(rng, &t, j, info[j], models)
	}
	if rng.Float64() < 0.05 {
		dsMarkError(rng, &t, info)
	}
	return t
}

func dsPickKind(rng *rand.Rand) int {
	switch x := rng.Float64(); {
	case x < 0.42:
		return dsLLM
	case x < 0.72:
		return dsTool
	case x < 0.82:
		return dsRetrieval
	case x < 0.92:
		return dsChain
	default:
		return dsAgent
	}
}

func dsMarkError(rng *rand.Rand, t *Trace, info []dsSpan) {
	var llms []int
	for j := range info {
		if info[j].kind == dsLLM {
			llms = append(llms, j)
		}
	}
	j := rng.IntN(len(t.Spans))
	if len(llms) > 0 && rng.Float64() < 0.7 {
		j = llms[rng.IntN(len(llms))]
	}
	sp := &t.Spans[j]
	sp.StatusCode = trace.StatusCodeError
	sp.StatusMessage = "upstream request failed"
	sp.Attributes = append(sp.Attributes, dsStr("error.type", "upstream_error"))
}

func (d *Dataset) fill(rng *rand.Rand, t *Trace, j int, s dsSpan, models []int) {
	sp := &t.Spans[j]
	switch s.kind {
	case dsAgent:
		name := dsAgentNames[rng.IntN(len(dsAgentNames))]
		sp.Name = "agent." + name
		sp.Attributes = []trace.Attr{dsStr("gen_ai.operation.name", "invoke_agent"), dsStr("gen_ai.agent.name", name)}
	case dsChain:
		sp.Name = "chain." + dsChainNames[rng.IntN(len(dsChainNames))]
		sp.Attributes = []trace.Attr{dsStr("gen_ai.operation.name", "invoke_workflow")}
	case dsRetrieval:
		sp.Name = "retrieval.search"
		sp.Attributes = []trace.Attr{
			dsStr("gen_ai.operation.name", "retrieval"),
			dsStr("http.route", dsRoutes[rng.IntN(len(dsRoutes))]), dsInt("retrieval.top_k", int64(3+rng.IntN(8))),
		}
	case dsTool:
		name := dsToolNames[rng.IntN(len(dsToolNames))]
		sp.Name = "tool." + name
		sp.Attributes = []trace.Attr{
			dsStr("gen_ai.operation.name", "execute_tool"), dsStr("gen_ai.tool.name", name),
			dsStr("http.route", dsRoutes[rng.IntN(len(dsRoutes))]),
		}
	case dsLLM:
		d.fillLLM(rng, sp, models)
	}
	if j == 0 {
		sp.Attributes = append(sp.Attributes, dsStr("session.id", t.Session), dsStr("user.id", t.User))
	}
}

func (d *Dataset) fillLLM(rng *rand.Rand, sp *trace.RawSpan, models []int) {
	m := d.models[models[rng.IntN(len(models))]]
	in := int64(max(1, dsLogNormal(rng, 800, 0.9)))
	out := int64(max(1, dsLogNormal(rng, 200, 0.8)))
	sp.Name = "chat " + m.name
	sp.SpanKind = 3 // client
	sp.Attributes = []trace.Attr{
		dsStr("gen_ai.operation.name", "chat"), dsStr("gen_ai.provider.name", m.provider),
		dsStr("gen_ai.request.model", m.name), dsStr("gen_ai.response.model", m.name),
		dsInt("gen_ai.usage.input_tokens", in), dsInt("gen_ai.usage.output_tokens", out),
		{Key: "gen_ai.request.temperature", Value: trace.DoubleValue(float64(rng.IntN(11)) / 10)},
		{Key: "gen_ai.response.finish_reasons", Value: trace.ArrayValue(trace.StringValue("stop"))},
	}
	if rng.Float64() < 0.15 {
		cached := int64(float64(in) * (0.3 + 0.4*rng.Float64()))
		sp.Attributes = append(sp.Attributes, dsInt("gen_ai.usage.cache_read.input_tokens", cached))
	}
	if rng.Float64() < 0.10 {
		total := 2048 + rng.IntN(6*1024+1)
		inLen := total * 7 / 10
		sp.Attributes = append(sp.Attributes,
			dsStr("gen_ai.input.messages", dsInputMessages(rng, inLen)),
			dsStr("gen_ai.output.messages", dsOutputMessages(rng, total-inLen)))
	}
}

// dsText returns about n bytes of word salad (always >= n).
func dsText(rng *rand.Rand, n int) string {
	var b strings.Builder
	b.Grow(n + 16)
	for b.Len() < n {
		b.WriteString(dsWords[rng.IntN(len(dsWords))])
		b.WriteByte(' ')
	}
	return strings.TrimSpace(b.String())
}

// dsMessagesJSON renders messages as OTel GenAI JSON totalling about size
// bytes; the text lengths are trimmed by the fixed JSON overhead.
func dsMessagesJSON(roles []string, texts []string, finish string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, role := range roles {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"role":%q,"parts":[{"type":"text","content":%q}]`, role, texts[i])
		if finish != "" {
			fmt.Fprintf(&b, `,"finish_reason":%q`, finish)
		}
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.String()
}

func dsInputMessages(rng *rand.Rand, size int) string {
	sys := size / 5
	return dsMessagesJSON([]string{"system", "user"},
		[]string{dsText(rng, sys), dsText(rng, max(1, size-sys-110))}, "")
}

func dsOutputMessages(rng *rand.Rand, size int) string {
	return dsMessagesJSON([]string{"assistant"}, []string{dsText(rng, max(1, size-90))}, "stop")
}

func dsLogNormal(rng *rand.Rand, median, sigma float64) float64 {
	return median * math.Exp(sigma*rng.NormFloat64())
}

func dsStr(k, v string) trace.Attr       { return trace.Attr{Key: k, Value: trace.StringValue(v)} }
func dsInt(k string, v int64) trace.Attr { return trace.Attr{Key: k, Value: trace.IntValue(v)} }
