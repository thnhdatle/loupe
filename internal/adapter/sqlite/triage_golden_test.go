// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// The usage-dedupe golden test of routed calls: the contract fixtures of testdata/contracts/triage
// (recorded from examples/triage, or hand-written, see the README there) go through the real
// write path (OTLP/HTTP receiver, ingestor, writer, SQLite store) and the queries service.

const triageDir = "../../../testdata/contracts/triage"

// triageCase is one fixture with what the write path must store for it.
type triageCase struct {
	file string
	// duplicates are the names of the spans flagged usage_duplicate, with the span count of
	// each name, per the usage-dedupe rule.
	duplicates map[string]int
	spans      int
	input      int64
	output     int64
	llmCalls   int64
	embCalls   int64
	llmModels  map[string]int64 // calls per model of kind llm
	embModels  map[string]int64
	// cost with the "strong" price table (fake-strong and fake-embed priced, fake-small not) and
	// with the "all" table (fake-small priced too), and the unpriced span counts of the trace.
	costStrong, costAll         int64
	unpricedStrong, unpricedAll int64
}

var triageCases = []triageCase{
	{ // (a) caller llm -> HTTP client -> route request -> one attempt
		file: "chat-single-attempt", spans: 4, input: 2, output: 6, llmCalls: 1,
		duplicates: map[string]int{"route request": 1, "chat fake-small": 1},
		llmModels:  map[string]int64{"fake-small": 1},
		// 2 in x 0.10 + 6 out x 0.20 USD per Mtok.
		costStrong: 0, unpricedStrong: 1, costAll: 1400,
	},
	{ // (b) a failed attempt without usage, then the successful one
		file: "chat-fallback", spans: 5, input: 4, output: 6, llmCalls: 1,
		duplicates: map[string]int{"route request": 1, "chat fake-small": 1, "chat fake-strong": 1},
		llmModels:  map[string]int64{"fake-strong": 1},
		// 4 in x 1.25 + 6 out x 10 USD per Mtok; Triage's own triage.cost_usd is 6.5e-05.
		costStrong: 65000, costAll: 65000,
	},
	{ // (b) cut stream: the cut attempt has no usage, the continuation reports the totals
		file: "chat-stream-continuation", spans: 5, input: 30, output: 40, llmCalls: 1,
		duplicates: map[string]int{"route request": 1, "chat fake-small": 1, "chat fake-strong": 1},
		llmModels:  map[string]int64{"fake-small": 1},
		// Triage's triage.cost_usd is 0.0004375: the unpriced caller (response model fake-small)
		// takes the priced continuation's cost through the unpriced route request. With
		// fake-small priced the caller keeps its own cost: 30 x 0.10 + 40 x 0.20.
		costStrong: 437500, costAll: 11000,
	},
	{ // (b) cut stream where both attempts report usage, summing to the route request's
		file: "chat-stream-continuation-usage", spans: 5, input: 30, output: 40, llmCalls: 1,
		duplicates: map[string]int{"route request": 1, "chat fake-small": 1, "chat fake-strong": 1},
		llmModels:  map[string]int64{"fake-small": 1},
		// The attempt with tokens on fake-small is unpriced, so the unpriced caller stays unpriced.
		costStrong: 0, unpricedStrong: 1, costAll: 11000,
	},
	{ // (c) caller embedding -> HTTP client -> route request (embedding) -> one attempt
		file: "embeddings-chain", spans: 4, input: 4, embCalls: 1,
		duplicates: map[string]int{"route request": 1, "embeddings fake-embed": 1},
		embModels:  map[string]int64{"fake-embed": 1},
		// 4 in x 0.02 USD per Mtok.
		costStrong: 80, costAll: 80,
	},
	{ // (d) mf's embeddings span under a chat route request: its own call
		file: "embeddings-under-chat-route", spans: 5, input: 8, output: 6, llmCalls: 1, embCalls: 1,
		duplicates: map[string]int{"route request": 1, "chat fake-small": 1},
		llmModels:  map[string]int64{"fake-small": 1},
		embModels:  map[string]int64{"fake-embed": 1},
		// the embedding: 6 in x 0.02; the caller as in (a).
		costStrong: 120, unpricedStrong: 1, costAll: 1520,
	},
}

func (c triageCase) cost(table string) (nanos, unpriced int64) {
	if table == "all" {
		return c.costAll, c.unpricedAll
	}
	return c.costStrong, c.unpricedStrong
}

// triageTables are the user price tables: USD per Mtok as integer nano-USD.
func triageTables(tb testing.TB) map[string]*cost.PriceTable {
	tb.Helper()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	price := func(model string, in, out int64) cost.Price {
		return cost.Price{
			Match: cost.MatchExact, Model: model, InputPerMtok: in, OutputPerMtok: out,
			EffectiveFrom: from, AsOf: from, Source: "test",
		}
	}
	strong := []cost.Price{
		price("fake-strong", 1_250_000_000, 10_000_000_000),
		price("fake-embed", 20_000_000, 0),
	}
	all := append(slices.Clone(strong), price("fake-small", 100_000_000, 200_000_000))
	out := map[string]*cost.PriceTable{}
	for name, user := range map[string][]cost.Price{"strong": strong, "all": all} {
		t, err := cost.NewPriceTable(nil, user)
		require.NoError(tb, err)
		out[name] = t
	}
	return out
}

// triagePipeline is the write path of cmd/loupe serve without the listeners.
type triagePipeline struct {
	store   *sqlite.Store
	path    string
	handler http.Handler
	queries *queries.Service
}

func newTriagePipeline(t *testing.T, table *cost.PriceTable) *triagePipeline {
	t.Helper()
	path := dbPath(t)
	clk := clock.System{}
	store := openAt(t, path, sqlite.WithClock(clk))
	w, err := writer.New(store, clk, writer.WithBatchSpans(1000), writer.WithBatchInterval(10*time.Millisecond))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	calc, err := cost.NewCalculator(table)
	require.NoError(t, err)
	ing, err := ingest.New(w, calc, clk, ingest.WithSyncAck(true))
	require.NoError(t, err)
	lim, err := ingest.NewInflightLimiter(ingest.DefaultMaxInflightBytes)
	require.NoError(t, err)
	srv, err := otlp.NewServer(otlp.DefaultConfig(), ing, lim)
	require.NoError(t, err)
	return &triagePipeline{
		store: store, path: path, handler: srv.HTTPHandler(),
		queries: queries.New(queries.Readers{Traces: store, Sessions: store, Rollups: store, Stats: store}, clk),
	}
}

// send posts one OTLP/JSON traces request; with sync_ack it returns once the batch committed.
func (p *triagePipeline) send(t *testing.T, body []byte) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// otlpSpan is one span of a fixture with the resource and scope it came from.
type otlpSpan struct {
	resource, scope json.RawMessage
	span            map[string]any
	id, parent      string
}

// field returns a string field of an OTLP/JSON object, empty when absent.
func field(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func loadTriageFixture(t *testing.T, name string) (all []byte, spans []otlpSpan) {
	t.Helper()
	all, err := os.ReadFile(filepath.Join(triageDir, name+".json"))
	require.NoError(t, err)
	var doc struct {
		ResourceSpans []struct {
			Resource   json.RawMessage `json:"resource"`
			ScopeSpans []struct {
				Scope json.RawMessage  `json:"scope"`
				Spans []map[string]any `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	require.NoError(t, json.Unmarshal(all, &doc))
	for _, rs := range doc.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				spans = append(spans, otlpSpan{
					resource: rs.Resource, scope: ss.Scope, span: s, id: field(s, "spanId"), parent: field(s, "parentSpanId"),
				})
			}
		}
	}
	return all, spans
}

// request wraps spans into OTLP/JSON requests, one per span, in the given order.
func requests(t *testing.T, spans []otlpSpan, order []int) [][]byte {
	t.Helper()
	out := make([][]byte, 0, len(order))
	for _, i := range order {
		s := spans[i]
		body, err := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{
			"resource":   s.resource,
			"scopeSpans": []any{map[string]any{"scope": s.scope, "spans": []any{s.span}}},
		}}})
		require.NoError(t, err)
		out = append(out, body)
	}
	return out
}

// depth is the number of ancestors of span i within the fixture.
func depth(spans []otlpSpan, i int) int {
	parents := map[string]string{}
	for _, s := range spans {
		parents[s.id] = s.parent
	}
	d := 0
	for p := spans[i].parent; p != ""; p = parents[p] {
		d++
	}
	return d
}

// arrivalOrders are the orders each trace's spans are sent in, one request per span: as
// recorded, reversed, ancestors first, attempts (leaves) first and seeded shuffles.
func arrivalOrders(spans []otlpSpan) map[string][]int {
	idx := make([]int, len(spans))
	for i := range idx {
		idx[i] = i
	}
	byDepth := func(sign int) []int {
		o := slices.Clone(idx)
		slices.SortStableFunc(o, func(a, b int) int { return sign * (depth(spans, a) - depth(spans, b)) })
		return o
	}
	orders := map[string][]int{
		"recorded":      idx,
		"reversed":      slices.Clone(idx),
		"ancestorFirst": byDepth(1),
		"attemptsFirst": byDepth(-1),
	}
	slices.Reverse(orders["reversed"])
	for seed := range uint64(4) {
		o := slices.Clone(idx)
		rand.New(rand.NewPCG(seed, 7)).Shuffle(len(o), func(i, j int) { o[i], o[j] = o[j], o[i] })
		orders["shuffle"+string(rune('0'+seed))] = o
	}
	return orders
}

func triageRange(t *testing.T, spans []otlpSpan) (from, to time.Time) {
	t.Helper()
	var first int64
	for _, s := range spans {
		ns, err := strconv.ParseInt(field(s.span, "startTimeUnixNano"), 10, 64)
		require.NoError(t, err)
		if first == 0 || ns < first {
			first = ns
		}
	}
	from = time.Unix(0, first).UTC().Truncate(time.Hour)
	return from, from.Add(3 * time.Hour)
}

func (p *triagePipeline) trace(t *testing.T, spans []otlpSpan) query.TraceDetail {
	t.Helper()
	d, err := p.queries.GetTrace(context.Background(), field(spans[0].span, "traceId"), false)
	require.NoError(t, err)
	return d
}

// assertCase checks everything expected of one stored fixture.
func (p *triagePipeline) assertCase(t *testing.T, c triageCase, table string, spans []otlpSpan) {
	t.Helper()
	d := p.trace(t, spans)
	assert.Len(t, d.Spans, c.spans)
	dup := map[string]int{}
	for _, sp := range d.Spans {
		if sp.UsageDuplicate {
			dup[sp.Name]++
		}
	}
	assert.Equal(t, c.duplicates, dup, "spans flagged usage_duplicate")
	assert.Equal(t, trace.Usage{Input: c.input, Output: c.output}, d.Summary.Usage, "tokens counted once")
	wantCost, wantUnpriced := c.cost(table)
	assert.Equal(t, wantCost, d.Summary.CostNanos, "cost counted once")
	assert.Equal(t, int(wantUnpriced), d.Summary.UnpricedSpans, "unpriced spans")

	// The call is counted at the caller span: the root of the fixture trace.
	root := d.Spans[0]
	for _, sp := range d.Spans {
		if sp.ParentSpanID == "" {
			root = sp
		}
	}
	assert.False(t, root.UsageDuplicate, "caller span %s is the kept span", root.Name)

	from, to := triageRange(t, spans)
	ctx := context.Background()
	ov, err := p.queries.Overview(ctx, query.OverviewRequest{
		From: from, To: to, GroupBy: query.GroupKind, Interval: query.IntervalHour,
	})
	require.NoError(t, err)
	assert.Equal(t, trace.Usage{Input: c.input, Output: c.output}, ov.Total.Usage, "overview tokens")
	assert.Equal(t, wantCost, ov.Total.CostNanos, "overview cost")
	assert.Equal(t, wantUnpriced, ov.Total.Unpriced, "overview unpriced")

	models, err := p.queries.ModelStats(ctx, from, to)
	require.NoError(t, err)
	got := map[string]int64{}
	for _, g := range models {
		if g.Stats.Requests > 0 {
			got[g.Key] = g.Stats.Requests
		}
	}
	assert.Equal(t, orEmpty(c.llmModels), got, "llm calls per model")

	assert.Equal(t, c.llmCalls, kindRequests(t, p.queries, from, to, trace.KindLLM), "llm requests: one per call")
	assert.Equal(t, c.embCalls, kindRequests(t, p.queries, from, to, trace.KindEmbedding),
		"embedding requests: one per call")

	embs, err := p.queries.Overview(ctx, query.OverviewRequest{
		From: from, To: to, GroupBy: query.GroupModel, Interval: query.IntervalHour, Kind: trace.KindEmbedding,
	})
	require.NoError(t, err)
	gotEmb := map[string]int64{}
	for _, g := range embs.Groups {
		if g.Stats.Requests > 0 {
			gotEmb[g.Key] = g.Stats.Requests
		}
	}
	assert.Equal(t, orEmpty(c.embModels), gotEmb, "embedding calls per model")
}

// kindRequests is the overview's request count of one span kind.
func kindRequests(t *testing.T, q *queries.Service, from, to time.Time, kind trace.Kind) int64 {
	t.Helper()
	ov, err := q.Overview(context.Background(), query.OverviewRequest{
		From: from, To: to, GroupBy: query.GroupNone, Interval: query.IntervalHour, Kind: kind,
	})
	require.NoError(t, err)
	return ov.Total.Requests
}

func orEmpty(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

func TestTriageContracts_UsageDedupeGolden(t *testing.T) {
	t.Parallel()
	tables := triageTables(t)
	for _, tableName := range []string{"strong", "all"} {
		for _, c := range triageCases {
			t.Run(tableName+"/"+c.file, func(t *testing.T) {
				t.Parallel()
				p := newTriagePipeline(t, tables[tableName])
				all, spans := loadTriageFixture(t, c.file)
				p.send(t, all)
				p.assertCase(t, c, tableName, spans)
			})
		}
	}
}

// TestTriageContracts_ArrivalOrderIndependent sends every span of a fixture as its own request
// in several orders: the stored spans, traces, sessions and rollups must equal those of
// the whole trace in one request, and the fixture expectations hold.
func TestTriageContracts_ArrivalOrderIndependent(t *testing.T) {
	t.Parallel()
	tables := triageTables(t)
	for _, tableName := range []string{"strong", "all"} {
		for _, c := range triageCases {
			t.Run(tableName+"/"+c.file, func(t *testing.T) {
				t.Parallel()
				ref := newTriagePipeline(t, tables[tableName])
				all, spans := loadTriageFixture(t, c.file)
				ref.send(t, all)
				want := snapshot(t, ref.path)
				for name, order := range arrivalOrders(spans) {
					p := newTriagePipeline(t, tables[tableName])
					for _, body := range requests(t, spans, order) {
						p.send(t, body)
					}
					assert.Equal(t, want, snapshot(t, p.path), "order %s %v", name, order)
					p.assertCase(t, c, tableName, spans)
				}
			})
		}
	}
}

// TestTriageContracts_AllTracesTogether stores every fixture in one database: the totals are
// the sums of the calls, whatever order the traces' spans arrive in.
func TestTriageContracts_AllTracesTogether(t *testing.T) {
	t.Parallel()
	table := triageTables(t)["strong"]
	var want struct {
		llmCalls, embCalls, input, output, cost, unpriced int64
	}
	var wantDupes int
	p := newTriagePipeline(t, table)
	everything := make([][]byte, 0, 64)
	var rangeSpans []otlpSpan
	for _, c := range triageCases {
		_, spans := loadTriageFixture(t, c.file)
		rangeSpans = append(rangeSpans, spans...)
		everything = append(everything, requests(t, spans, arrivalOrders(spans)["attemptsFirst"])...)
		wantCost, wantUnpriced := c.cost("strong")
		want.llmCalls += c.llmCalls
		want.embCalls += c.embCalls
		want.input, want.output = want.input+c.input, want.output+c.output
		want.cost, want.unpriced = want.cost+wantCost, want.unpriced+wantUnpriced
		for _, n := range c.duplicates {
			wantDupes += n
		}
	}
	for _, i := range rand.New(rand.NewPCG(1, 2)).Perm(len(everything)) {
		p.send(t, everything[i])
	}
	from, to := triageRange(t, rangeSpans)
	ov, err := p.queries.Overview(context.Background(), query.OverviewRequest{
		From: from, To: to, GroupBy: query.GroupNone, Interval: query.IntervalHour,
	})
	require.NoError(t, err)
	assert.Equal(t, want.llmCalls, kindRequests(t, p.queries, from, to, trace.KindLLM))
	assert.Equal(t, want.embCalls, kindRequests(t, p.queries, from, to, trace.KindEmbedding))
	assert.Equal(t, trace.Usage{Input: want.input, Output: want.output}, ov.Total.Usage)
	assert.Equal(t, want.cost, ov.Total.CostNanos)
	assert.Equal(t, want.unpriced, ov.Total.Unpriced)
	db := rawDB(t, p.path)
	assert.Equal(t, int64(wantDupes), queryInt(t, db, "SELECT count(*) FROM spans WHERE flags & 2 != 0"),
		"duplicates stored")
}
