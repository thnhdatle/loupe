// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/app/analyze"
	"github.com/thnhdatle/loupe/internal/domain/diagnose"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const analyzePath = "/v1/traces/4bf92f3577b34da6a3ce929d0e0e4736/analyze"

type fakeAnalyzer struct {
	report analyze.Report
	err    error
	id     string
}

func (f *fakeAnalyzer) Analyze(_ context.Context, id string) (analyze.Report, error) {
	f.id = id
	return f.report, f.err
}

func (f *fakeAnalyzer) Timeout() time.Duration { return time.Minute }

func TestAnalyzeTrace(t *testing.T) {
	t.Parallel()
	a := &fakeAnalyzer{report: analyze.Report{
		TraceID:  "4bf92f3577b34da6a3ce929d0e0e4736",
		Findings: []diagnose.Finding{{Kind: diagnose.KindError, Text: `"x" failed`, SpanIDs: []string{"a"}}},
		Causes:   []analyze.Point{{Text: "boom", SpanIDs: []string{"a"}}},
		Fixes:    []analyze.Point{},
	}}
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{Analyzer: a})
	rec := do(t, h, http.MethodPost, analyzePath, map[string]string{"Authorization": "Bearer " + testToken})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", a.id)
	assert.JSONEq(t, `{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736",
		"findings":[{"kind":"error","text":"\"x\" failed","span_ids":["a"]}],
		"causes":[{"text":"boom","span_ids":["a"]}],"fixes":[],"spans_omitted":0,"notice":""}`, rec.Body.String())
}

func TestAnalyzeTrace_Errors(t *testing.T) {
	t.Parallel()
	auth := map[string]string{"Authorization": "Bearer " + testToken}
	tests := []struct {
		name   string
		err    error
		target string
		hdr    map[string]string
		status int
		code   string
	}{
		{"busy", analyze.ErrBusy, analyzePath, auth, http.StatusConflict, "analyze_busy"},
		{"not found", trace.ErrNotFound, analyzePath, auth, http.StatusNotFound, "not_found"},
		{"bad id", trace.ErrInvalidID, analyzePath, auth, http.StatusBadRequest, "invalid_id"},
		{"unknown parameter", nil, analyzePath + "?x=1", auth, http.StatusBadRequest, "invalid_parameter"},
		{"no token", nil, analyzePath, nil, http.StatusUnauthorized, "unauthorized"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{Analyzer: &fakeAnalyzer{err: tc.err}})
			rec := do(t, h, http.MethodPost, tc.target, tc.hdr)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Equal(t, tc.code, errorCode(t, rec))
		})
	}
}

func TestAnalyzeTrace_CookieIsNotEnough(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{
		Analyzer: &fakeAnalyzer{}, SessionAuth: func(*http.Request) bool { return true },
	})
	assert.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodPost, analyzePath, nil).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, get(t, h, analyzePath).Code)
}
