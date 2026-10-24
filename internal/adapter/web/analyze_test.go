// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/web"
	"github.com/thnhdatle/loupe/internal/app/analyze"
	"github.com/thnhdatle/loupe/internal/domain/diagnose"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const analyzeTarget = "/traces/" + traceID + "/analyze"

type fakeAnalyzer struct {
	report analyze.Report
	err    error
	calls  int
}

func (f *fakeAnalyzer) Analyze(context.Context, string) (analyze.Report, error) {
	f.calls++
	return f.report, f.err
}

func (f *fakeAnalyzer) Timeout() time.Duration { return time.Minute }

func rateLimitReport() analyze.Report {
	return analyze.Report{
		TraceID: traceID,
		Findings: []diagnose.Finding{{
			Kind: diagnose.KindRateLimit, Text: "1 call was rate limited: RateLimit",
			SpanIDs: []string{"a000000000000002"},
		}},
		Causes: []analyze.Point{{
			Text: "The provider refused the call (rate limit).", SpanIDs: []string{"a000000000000002"},
		}},
		Fixes: []analyze.Point{{
			Text: "Retry with backoff.", SpanIDs: []string{"a000000000000002", "a000000000000004", "gone"},
		}},
		SpansOmitted: 2,
	}
}

func TestAnalyze_ButtonOnlyWhenEnabled(t *testing.T) {
	t.Parallel()
	off := newEnv(t, web.Options{})
	assert.NotContains(t, body(off.get("/traces/"+traceID)), "Analyze")

	on := newEnv(t, web.Options{Analyzer: &fakeAnalyzer{}})
	b := body(on.get("/traces/" + traceID))
	assert.Contains(t, b, `hx-post="/traces/`+traceID+`/analyze"`)
	assert.Contains(t, b, `<button type="submit">Analyze</button>`)
	assert.Contains(t, b, `<div id="analysis"></div>`)
}

func TestAnalyze_Fragment(t *testing.T) {
	t.Parallel()
	a := &fakeAnalyzer{report: rateLimitReport()}
	e := newEnv(t, web.Options{Analyzer: a})
	rec := e.post(analyzeTarget, nil, withHeader("HX-Request", "true"))
	require.Equal(t, http.StatusOK, rec.Code)
	b := body(rec)
	assert.NotContains(t, b, "<html")
	for _, want := range []string{
		"<h3>Cause</h3>", "The provider refused the call (rate limit).", "<h3>Fix</h3>", "Retry with backoff.",
		"<h3>Findings</h3>", "1 call was rate limited: RateLimit",
		`<a href="/traces/` + traceID + `?span=a000000000000002" hx-get="/traces/` + traceID +
			`?span=a000000000000002" hx-target="#detail" hx-swap="outerHTML" hx-push-url="true" title="llm.call">2</a>`,
		`title="llm.retry">3</a>`, "2 spans were not sent to the model",
	} {
		assert.Contains(t, b, want)
	}
	assert.Equal(t, 1, a.calls)
}

func TestAnalyze_FullPageWithoutHTMX(t *testing.T) {
	t.Parallel()
	report := rateLimitReport()
	report.Causes, report.Fixes = []analyze.Point{}, []analyze.Point{}
	report.Notice = "The model did not return a valid answer. Showing the findings only."
	e := newEnv(t, web.Options{Analyzer: &fakeAnalyzer{report: report}})
	rec := e.post(analyzeTarget, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	b := body(rec)
	for _, want := range []string{"<html", `class="wrow"`, report.Notice, "1 call was rate limited"} {
		assert.Contains(t, b, want)
	}
	assert.NotContains(t, b, "<h3>Cause</h3>")
}

func TestAnalyze_Failures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		msg    string
	}{
		{"busy", analyze.ErrBusy, http.StatusConflict, "An analysis of this trace is already running."},
		{"not found", trace.ErrNotFound, http.StatusNotFound, "Not found."},
		{"internal", errors.New("disk on fire"), http.StatusInternalServerError, "Analysis failed; see the server log."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, web.Options{Analyzer: &fakeAnalyzer{err: tc.err}})
			rec := e.post(analyzeTarget, nil, withHeader("HX-Request", "true"))
			assert.Equal(t, http.StatusOK, rec.Code, "htmx swaps only successful responses")
			assert.Contains(t, body(rec), tc.msg)
			assert.NotContains(t, body(rec), "<html")

			rec = e.post(analyzeTarget, nil)
			assert.Equal(t, tc.status, rec.Code)
			assert.Contains(t, body(rec), tc.msg)
		})
	}
}

func TestAnalyze_Guards(t *testing.T) {
	t.Parallel()
	a := &fakeAnalyzer{report: rateLimitReport()}
	e := newEnv(t, web.Options{Analyzer: a})
	rec := e.post(analyzeTarget, nil, withHeader("Origin", "https://evil.example"))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, http.StatusNotFound, e.get(analyzeTarget).Code)

	login := newEnv(t, web.Options{Analyzer: a, RequireLogin: true})
	assert.Equal(t, http.StatusSeeOther, login.post(analyzeTarget, nil).Code)
	assert.Zero(t, a.calls)

	off := newEnv(t, web.Options{})
	assert.Equal(t, http.StatusNotFound, off.post(analyzeTarget, nil).Code)
}
