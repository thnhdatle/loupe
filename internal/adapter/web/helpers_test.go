// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/web"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const (
	testToken = "s3cret-token"
	traceID   = "0102030405060708090a0b0c0d0e0f10"
	secretMsg = "SECRET PROMPT TEXT"
)

var epoch = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// seqReader is a deterministic, non-repeating randomness source.
type seqReader struct{ n uint64 }

func (s *seqReader) Read(p []byte) (int, error) {
	for i := range p {
		if i%8 == 0 {
			s.n++
		}
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], s.n*0x9e3779b97f4a7c15+uint64(i))
		p[i] = b[i%8]
	}
	return len(p), nil
}

// fakeQueries returns canned results and records what it was asked.
type fakeQueries struct {
	traceReq   queries.TraceListRequest
	tracePage  query.TracePage
	detail     query.TraceDetail
	detailFull query.TraceDetail
	getCalls   []bool
	sessReq    queries.SessionListRequest
	sessPage   query.SessionPage
	sessID     string
	sessPg     query.Page
	sessDetail query.SessionDetail
	overReq    query.OverviewRequest
	overview   queries.Overview
	models     []queries.GroupStats
	ingest     query.IngestStats
	ingestErr  error
	ingestFrom time.Time
	ingestTo   time.Time
	err        error
}

func (f *fakeQueries) ListTraces(_ context.Context, r queries.TraceListRequest) (query.TracePage, error) {
	f.traceReq = r
	return f.tracePage, f.err
}

func (f *fakeQueries) GetTrace(_ context.Context, _ string, inc bool) (query.TraceDetail, error) {
	f.getCalls = append(f.getCalls, inc)
	if inc && f.detailFull.Spans != nil {
		return f.detailFull, f.err
	}
	return f.detail, f.err
}

func (f *fakeQueries) ListSessions(_ context.Context, r queries.SessionListRequest) (query.SessionPage, error) {
	f.sessReq = r
	return f.sessPage, f.err
}

func (f *fakeQueries) GetSession(_ context.Context, id string, p query.Page) (query.SessionDetail, error) {
	f.sessID, f.sessPg = id, p
	return f.sessDetail, f.err
}

func (f *fakeQueries) Overview(_ context.Context, r query.OverviewRequest) (queries.Overview, error) {
	f.overReq = r
	return f.overview, f.err
}

func (f *fakeQueries) ModelStats(context.Context, time.Time, time.Time) ([]queries.GroupStats, error) {
	return f.models, f.err
}

func (f *fakeQueries) IngestInRange(_ context.Context, from, to time.Time) (query.IngestStats, error) {
	f.ingestFrom, f.ingestTo = from, to
	return f.ingest, f.ingestErr
}

func i64(v int64) *int64 { return &v }

func str(s string) *string { return &s }

// fixtures returns fake queries filled with data for every page.
func fixtures() *fakeQueries {
	sum := query.TraceSummary{
		TraceID: traceID, RootName: "chat-request", Service: "api", StartNs: epoch.UnixNano(),
		EndNs: epoch.Add(2 * time.Second).UnixNano(), DurationNs: int64(2 * time.Second), SpanCount: 4,
		Models: []string{"gpt-4o", "claude-3"}, Usage: trace.Usage{Input: 1200, Output: 300},
		CostNanos: 12_500_000, UnpricedSpans: 1, HasError: true, SessionID: "sess-1", UserID: "u1",
	}
	spans := []query.SpanView{
		{
			SpanID: "a000000000000001", Name: "chat-request", Kind: trace.KindAgent, Status: trace.StatusOK,
			Service: "api", StartNs: epoch.UnixNano(), EndNs: epoch.Add(2 * time.Second).UnixNano(),
			DurationNs: int64(2 * time.Second), Attributes: json.RawMessage(`{"http.route":"/chat","n":3}`),
		},
		{
			SpanID: "a000000000000002", ParentSpanID: "a000000000000001", Name: "llm.call", Kind: trace.KindLLM,
			Status: trace.StatusError, ErrorType: "RateLimit", Service: "api", Model: "gpt-4o", Provider: "openai",
			StartNs: epoch.Add(100 * time.Millisecond).UnixNano(), EndNs: epoch.Add(1500 * time.Millisecond).UnixNano(),
			DurationNs: int64(1400 * time.Millisecond), HasUsage: true, Usage: trace.Usage{Input: 1200, Output: 300},
			Attributes: json.RawMessage(`{"gen_ai.request.model":"gpt-4o"}`),
			Content:    []query.ContentPartView{{Role: "user", Bytes: 18}, {Role: "assistant", Bytes: 5}},
		},
		{
			SpanID: "a000000000000003", ParentSpanID: "a000000000000001", Name: "search", Kind: trace.KindTool,
			Status: trace.StatusOK, Service: "api", StartNs: epoch.Add(1600 * time.Millisecond).UnixNano(),
			EndNs: epoch.Add(1700 * time.Millisecond).UnixNano(), DurationNs: int64(100 * time.Millisecond),
			ContentStripped: true, Content: []query.ContentPartView{{Role: "tool", Bytes: 40}},
		},
		{
			SpanID: "a000000000000004", ParentSpanID: "a000000000000002", Name: "llm.retry", Kind: trace.KindLLM,
			Status: trace.StatusOK, StartNs: epoch.Add(200 * time.Millisecond).UnixNano(),
			EndNs: epoch.Add(300 * time.Millisecond).UnixNano(), DurationNs: int64(100 * time.Millisecond),
			UsageDuplicate: true, HasUsage: true, Usage: trace.Usage{Input: 5}, CostNanos: i64(1000),
		},
	}
	full := query.TraceDetail{Summary: sum, Spans: append([]query.SpanView(nil), spans...)}
	full.Spans[1].Content = []query.ContentPartView{
		{Role: "user", Bytes: 18, Body: str(secretMsg + " <b>x</b>")},
		{Role: "assistant", Bytes: 5, Body: str("hello"), Truncated: true},
	}
	// The first fetch carries markers only (bodies not requested).
	stored := query.TraceDetail{Summary: sum, Spans: append([]query.SpanView(nil), spans...)}
	stored.Spans[2].ContentStripped = true
	series := []queries.SeriesPoint{
		{Bucket: epoch, Key: "gpt-4o", Stats: queries.Stats{
			Requests: 3, CostNanos: 3e9, P95Ns: 2e9,
			Usage: trace.Usage{Input: 100, Output: 50},
		}},
		{Bucket: epoch.Add(time.Hour), Key: "gpt-4o", Stats: queries.Stats{Requests: 2, CostNanos: 1e9, P95Ns: 1e9}},
		{Bucket: epoch.Add(time.Hour), Key: "claude-3", Stats: queries.Stats{Requests: 1, CostNanos: 5e8, P95Ns: 3e8}},
		{Bucket: epoch, Key: "zzz-not-charted-model", Other: true},
	}
	groups := []queries.GroupStats{
		{Key: "gpt-4o", Stats: queries.Stats{
			Requests: 5, Errors: 1, CostNanos: 4e9, Unpriced: 2,
			Usage: trace.Usage{Input: 1000, Output: 400}, P50Ns: 5e8, P95Ns: 2e9, P99Ns: 3e9,
		}},
		{Key: "claude-3", Stats: queries.Stats{Requests: 1, CostNanos: 5e8, P50Ns: 3e8, P95Ns: 3e8, P99Ns: 3e8}},
		{Key: ""},
	}
	return &fakeQueries{
		tracePage: query.TracePage{Data: []query.TraceSummary{sum}, NextCursor: "CURSOR1"},
		detail:    stored, detailFull: full,
		sessPage: query.SessionPage{Data: []query.SessionSummary{{
			ID: "sess-1", UserID: "u1", FirstNs: epoch.UnixNano(), LastNs: epoch.Add(time.Minute).UnixNano(),
			TraceCount: 2, Usage: trace.Usage{Input: 10, Output: 5}, CostNanos: 2_000_000,
		}}, NextCursor: "SC1"},
		sessDetail: query.SessionDetail{
			Summary: query.SessionSummary{
				ID: "sess-1", UserID: "u1", FirstNs: epoch.UnixNano(),
				LastNs: epoch.Add(time.Minute).UnixNano(), TraceCount: 2, CostNanos: 2_000_000,
			},
			Data:       []query.TraceSummary{sum, {TraceID: "ff", RootName: "", StartNs: epoch.Add(time.Minute).UnixNano()}},
			NextCursor: "SD1",
		},
		overview: queries.Overview{
			From: epoch, To: epoch.Add(2 * time.Hour), GroupBy: query.GroupModel, Interval: query.IntervalHour,
			Total: queries.Stats{
				Requests: 6, Errors: 1, CostNanos: 4_500_000_000, Unpriced: 2, P95Ns: 2e9,
				Usage: trace.Usage{Input: 1000, Output: 500},
			},
			Groups: groups, Series: series,
		},
		models: groups,
		ingest: query.IngestStats{Rejected: map[string]int64{"too_large": 3}, Dropped: map[string]int64{"queue_full": 2}},
	}
}

type env struct {
	h   *web.Handler
	q   *fakeQueries
	clk *clock.Fake
}

func newEnv(t *testing.T, opts web.Options) *env {
	t.Helper()
	q := fixtures()
	clk := clock.NewFake(epoch)
	opts.Clock, opts.Rand = clk, &seqReader{}
	if opts.Tokens == nil {
		opts.Tokens = []string{testToken}
	}
	h, err := web.NewHandler(q, opts)
	require.NoError(t, err)
	return &env{h: h, q: q, clk: clk}
}

func (e *env) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) get(target string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
	for _, m := range mods {
		m(req)
	}
	return e.do(req)
}

func (e *env) post(target string, form url.Values, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, target,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, m := range mods {
		m(req)
	}
	return e.do(req)
}

func withCookie(c *http.Cookie) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(c) }
}

func withHeader(k, v string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

func withAddr(addr string) func(*http.Request) {
	return func(r *http.Request) { r.RemoteAddr = addr }
}

func body(rec *httptest.ResponseRecorder) string {
	return rec.Body.String()
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == web.SessionCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie in response", web.SessionCookie)
	return nil
}

// login signs in with the API token and returns the session cookie.
func (e *env) login(t *testing.T) *http.Cookie {
	t.Helper()
	rec := e.post("/login", url.Values{"token": {testToken}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	return sessionCookie(t, rec)
}
