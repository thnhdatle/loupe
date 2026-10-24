// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/web"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestHandler_Overview(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	rec := e.get("/")
	require.Equal(t, http.StatusOK, rec.Code)
	b := body(rec)
	for _, want := range []string{
		"<h1>Overview</h1>", "Requests", ">6<", "Error rate", "16.7%", "$4.50", "2 unpriced", "p95 latency", "2.00 s",
		`data-chart="cost"`, `data-chart="tokens"`, `data-chart="p95"`, `id="chart-data"`,
		"3 spans rejected and 2 dropped between", "2026-10-18 12:00:00", "rejected too_large: 3", "dropped queue_full: 2",
		`href="?range=7d"`, `aria-current="true"`, `<meta name="htmx-config"`,
		`"includeIndicatorStyles":false,"allowEval":false,"allowScriptTags":false`,
	} {
		assert.Contains(t, b, want)
	}
	assert.NotContains(t, b, "zzz-not-charted-model")
	assert.Equal(t, query.GroupModel, e.q.overReq.GroupBy)
	assert.Equal(t, trace.KindLLM, e.q.overReq.Kind)
	assert.Equal(t, query.IntervalHour, e.q.overReq.Interval)
	assert.Equal(t, epoch, e.q.overReq.To)
	assert.Equal(t, e.q.overReq.From, e.q.ingestFrom, "the banner counts the effective range")
	assert.Equal(t, epoch, e.q.ingestTo)
	assert.Equal(t, epoch.Add(-24*3600e9), e.q.overReq.From)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	chart := regexp.MustCompile(`(?s)<script type="application/json" id="chart-data" nonce="[^"]+">(.*?)</script>`).
		FindStringSubmatch(b)
	require.Len(t, chart, 2)
	assert.Contains(t, chart[1], `"models":["gpt-4o","claude-3",""]`)
	assert.Contains(t, chart[1], `"cost":[[3,1],[null,0.5],[null,null]]`)
	assert.Contains(t, chart[1], `"p95":[[2000,1000],[null,300],[null,null]]`)
}

func TestHandler_Overview_RangeAndEmpty(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	e.q.overview.Total.Requests = 0
	e.q.ingest = query.IngestStats{}
	rec := e.get("/?range=30d")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body(rec), "No LLM spans in this range.")
	assert.NotContains(t, body(rec), "were not stored")
	assert.Equal(t, query.IntervalDay, e.q.overReq.Interval)

	rec = e.get("/?from=2026-10-18T10:00&to=2026-10-18T12:30")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "2026-10-18T10:00:00Z", e.q.overReq.From.Format("2006-01-02T15:04:05Z07:00"))
	assert.Contains(t, body(rec), `value="2026-10-18T10:00"`)

	for _, bad := range []string{"/?from=nonsense", "/?from=2026-10-18T10:00&to=2026-10-18T09:00", "/?to=x"} {
		assert.Equal(t, http.StatusBadRequest, e.get(bad).Code, bad)
	}
	e.q.ingestErr = errors.New("boom")
	assert.Equal(t, http.StatusOK, e.get("/").Code, "ingest stats failure only drops the banner")
}

func TestHandler_Traces(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	rec := e.get("/traces?model=gpt-4o&kind=llm&status=error&min_cost=0.5&min_duration_ms=20&has_error=1" +
		"&name=chat&sort=cost&order=asc&from=2026-10-18T10:00&service=api&provider=openai&session_id=s&user_id=u")
	require.Equal(t, http.StatusOK, rec.Code)
	b := body(rec)
	for _, want := range []string{
		"<h1>Traces</h1>", `href="/traces/` + traceID + `"`, "chat-request", "api", "gpt-4o, claude-3", "2.00 s",
		"1,500", "$0.0125", "(1 unpriced)", `class="status status-error"`, "2026-10-19 12:00:00",
		`rel="next"`, "cursor=CURSOR1", "model=gpt-4o", "min_cost=0.5", "has_error=1", "sort=cost",
		`name="model" value="gpt-4o"`, `<option value="cost" selected>`, ` checked>`, `hx-push-url="true"`,
	} {
		assert.Contains(t, b, want)
	}
	req := e.q.traceReq
	assert.Equal(t, "gpt-4o", req.Filter.Span.Model)
	assert.Equal(t, trace.KindLLM, req.Filter.Span.Kind)
	assert.Equal(t, trace.StatusError, req.Filter.Span.Status)
	assert.Equal(t, "chat", req.Filter.Span.NamePrefix)
	assert.Equal(t, int64(20_000_000), req.Filter.Span.MinDurationNs)
	require.NotNil(t, req.Filter.MinCostNanos)
	assert.Equal(t, int64(500_000_000), *req.Filter.MinCostNanos)
	require.NotNil(t, req.Filter.HasError)
	assert.True(t, *req.Filter.HasError)
	assert.Equal(t, query.Sort("cost"), req.Sort)
	assert.Equal(t, query.Order("asc"), req.Order)
	assert.Equal(t, epoch.Add(-26*3600e9), req.Filter.From)
	assert.Equal(t, 50, req.Page.Limit)

	e.get("/traces?cursor=ABC")
	assert.Equal(t, "ABC", e.q.traceReq.Page.Cursor)
}

func TestHandler_Traces_Fragment(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	rec := e.get("/traces", withHeader("HX-Request", "true"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body(rec), `id="results"`)
	assert.NotContains(t, body(rec), "<html")
	assert.Contains(t, rec.Header().Values("Vary"), "HX-Request")

	rec = e.get("/traces", withHeader("HX-Request", "true"), withHeader("HX-History-Restore-Request", "true"))
	assert.Contains(t, body(rec), "<html")

	e.q.tracePage = query.TracePage{}
	rec = e.get("/traces")
	assert.Contains(t, body(rec), "No traces match these filters.")
	assert.NotContains(t, body(rec), `rel="next"`)
}

func TestHandler_Traces_BadInput(t *testing.T) {
	t.Parallel()
	for _, q := range []string{
		"from=bad", "to=bad", "min_cost=abc", "min_duration_ms=-1", "min_duration_ms=x",
	} {
		e := newEnv(t, web.Options{})
		rec := e.get("/traces?" + q)
		assert.Equal(t, http.StatusBadRequest, rec.Code, q)
		assert.Contains(t, body(rec), "400")
	}
	e := newEnv(t, web.Options{})
	e.q.err = query.ErrInvalidFilter
	assert.Equal(t, http.StatusBadRequest, e.get("/traces").Code)
	e.q.err = errors.New("db exploded: secret detail")
	rec := e.get("/traces")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, body(rec), "secret detail")
}

func TestHandler_TraceDetail(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	rec := e.get("/traces/" + traceID)
	require.Equal(t, http.StatusOK, rec.Code)
	b := body(rec)
	for _, want := range []string{
		"<h1>chat-request</h1>", traceID, `class="wrow"`, "llm.call", "llm.retry", "search", `kind kind-llm`,
		">LLM<", ">TOOL<",
		">AGT<", `<rect class="bar-llm"`, "1.40 s", "100 ms", `depth-2`, `href="/sessions/sess-1"`,
		`id="span-panel"`, "chat-request", "http.route", "/chat",
	} {
		assert.Contains(t, b, want)
	}
	assert.Equal(t, []bool{false, true}, e.q.getCalls[:2], "content is fetched because it is stored")

	rec = e.get("/traces/"+traceID+"?span=a000000000000002", withHeader("HX-Request", "true"))
	require.Equal(t, http.StatusOK, rec.Code)
	b = body(rec)
	assert.NotContains(t, b, "<html")
	for _, want := range []string{
		"RateLimit", "gpt-4o", "openai", "Input", "1,200", "Cache read", "unpriced", `class="role">user<`,
		`class="role">assistant<`, secretMsg, "&lt;b&gt;x&lt;/b&gt;", "18 bytes", "5 bytes, truncated",
		"gen_ai.request.model", `aria-current="true"`,
	} {
		assert.Contains(t, b, want)
	}
	assert.NotContains(t, b, "<b>x</b>")
	assert.NotContains(t, b, web.ContentStrippedMessage)
	assert.Equal(t, []bool{false, true}, e.q.getCalls[2:4])
}

func TestHandler_TraceDetail_StrippedAndDuplicate(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	e.q.detail.Spans[1].Content = nil
	rec := e.get("/traces/" + traceID + "?span=a000000000000003")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body(rec), "content not stored: set content.capture=on")
	assert.Contains(t, body(rec), "40 bytes")
	assert.Equal(t, []bool{false}, e.q.getCalls, "no content fetch when nothing is stored")

	rec = e.get("/traces/" + traceID + "?span=a000000000000004")
	assert.Contains(t, body(rec), "Duplicate usage")
	assert.Contains(t, body(rec), "$0.000001")
	assert.Contains(t, body(rec), "No content captured for this span.")

	rec = e.get("/traces/" + traceID + "?span=does-not-exist")
	assert.Equal(t, http.StatusOK, rec.Code, "an unknown span selects the first one")
}

func TestHandler_TraceDetail_Errors(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	e.q.err = trace.ErrNotFound
	assert.Equal(t, http.StatusNotFound, e.get("/traces/"+traceID).Code)
	e.q.err = trace.ErrInvalidID
	assert.Equal(t, http.StatusNotFound, e.get("/traces/zz").Code)
	e.q.err = nil
	e.q.detail.Spans = nil
	assert.Equal(t, http.StatusNotFound, e.get("/traces/"+traceID).Code)
}

func TestHandler_Sessions(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	rec := e.get("/sessions?user_id=u1&range=7d")
	require.Equal(t, http.StatusOK, rec.Code)
	b := body(rec)
	for _, want := range []string{
		"<h1>Sessions</h1>", `href="/sessions/sess-1"`, "u1", "$0.002000", "cursor=SC1", "user_id=u1", "range=7d",
	} {
		assert.Contains(t, b, want)
	}
	assert.Equal(t, "u1", e.q.sessReq.Filter.UserID)

	rec = e.get("/sessions/sess-1?cursor=X")
	require.Equal(t, http.StatusOK, rec.Code)
	b = body(rec)
	for _, want := range []string{
		"<h1>Session sess-1</h1>", "Timeline", `href="/traces/` + traceID + `"`, `href="/traces/ff"`, "(unnamed)",
		"cursor=SD1", `<rect class="bar-llm"`,
	} {
		assert.Contains(t, b, want)
	}
	assert.Equal(t, "sess-1", e.q.sessID)
	assert.Equal(t, "X", e.q.sessPg.Cursor)

	e.q.sessPage = query.SessionPage{}
	assert.Contains(t, body(e.get("/sessions")), "No sessions in this range.")
	e.q.err = trace.ErrNotFound
	assert.Equal(t, http.StatusNotFound, e.get("/sessions/nope").Code)
	assert.Equal(t, http.StatusBadRequest, newEnv(t, web.Options{}).get("/sessions?from=x").Code)
}

func TestHandler_Models(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	rec := e.get("/models")
	require.Equal(t, http.StatusOK, rec.Code)
	b := body(rec)
	for _, want := range []string{
		"<h1>Models</h1>", "gpt-4o", "claude-3", "(unknown)", "20.0%", "$4.00", "(2 unpriced)", "500 ms", "3.00 s",
		`scope="col"`,
	} {
		assert.Contains(t, b, want)
	}
	e.q.models = nil
	assert.Contains(t, body(e.get("/models")), "No LLM spans in this range.")
	e.q.err = errors.New("x")
	assert.Equal(t, http.StatusInternalServerError, e.get("/models").Code)
	assert.Equal(t, http.StatusBadRequest, newEnv(t, web.Options{}).get("/models?to=x").Code)
}

func TestHandler_StaticAndMisc(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	for path, ct := range map[string]string{
		"/static/app.css": "text/css", "/static/app.js": "text/javascript",
		"/static/vendor/htmx.min.js": "text/javascript", "/static/vendor/uPlot.iife.min.js": "text/javascript",
		"/static/vendor/uPlot.min.css": "text/css",
	} {
		rec := e.get(path)
		assert.Equal(t, http.StatusOK, rec.Code, path)
		assert.Contains(t, rec.Header().Get("Content-Type"), ct, path)
		assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "default-src 'self'")
	}
	assert.Equal(t, http.StatusNotFound, e.get("/static/missing.js").Code)
	assert.Equal(t, http.StatusNoContent, e.get("/favicon.ico").Code)
	rec := e.get("/nope")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, body(rec), "Page not found.")
	assert.Equal(t, http.StatusNotFound, e.post("/", nil).Code)
}

func TestHandler_NeverLogsContent(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	e := newEnv(t, web.Options{Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	rec := e.get("/traces/" + traceID + "?span=a000000000000002")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body(rec), secretMsg, "content is rendered in the page")
	e.q.err = errors.New("storage failure")
	assert.Equal(t, http.StatusInternalServerError, e.get("/traces/"+traceID).Code)
	e.q.err = nil
	e.q.ingestErr = errors.New("ingest failure")
	e.get("/")
	assert.Contains(t, logs.String(), "storage failure")
	assert.NotContains(t, logs.String(), secretMsg)
}
