// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
)

const testToken = "s3cret"

type fakeQueries struct {
	traceReq   queries.TraceListRequest
	tracePage  query.TracePage
	traceID    string
	traceInc   bool
	detail     query.TraceDetail
	sessionReq queries.SessionListRequest
	sessPage   query.SessionPage
	sessionID  string
	sessionPg  query.Page
	sessDetail query.SessionDetail
	overReq    query.OverviewRequest
	overview   queries.Overview
	topReq     query.TopSpansRequest
	top        []query.TopSpan
	ingest     query.IngestStats
	err        error
}

func (f *fakeQueries) ListTraces(_ context.Context, r queries.TraceListRequest) (query.TracePage, error) {
	f.traceReq = r
	return f.tracePage, f.err
}

func (f *fakeQueries) GetTrace(_ context.Context, id string, inc bool) (query.TraceDetail, error) {
	f.traceID, f.traceInc = id, inc
	return f.detail, f.err
}

func (f *fakeQueries) ListSessions(_ context.Context, r queries.SessionListRequest) (query.SessionPage, error) {
	f.sessionReq = r
	return f.sessPage, f.err
}

func (f *fakeQueries) GetSession(_ context.Context, id string, p query.Page) (query.SessionDetail, error) {
	f.sessionID, f.sessionPg = id, p
	return f.sessDetail, f.err
}

func (f *fakeQueries) Overview(_ context.Context, r query.OverviewRequest) (queries.Overview, error) {
	f.overReq = r
	return f.overview, f.err
}

func (f *fakeQueries) TopSpans(_ context.Context, r query.TopSpansRequest) ([]query.TopSpan, error) {
	f.topReq = r
	return f.top, f.err
}

func (f *fakeQueries) IngestStats(context.Context) (query.IngestStats, error) { return f.ingest, f.err }

type fakeDeleter struct {
	user string
	id   string
	err  error
}

func (d *fakeDeleter) DeleteUser(user string) (string, error) {
	d.user = user
	return d.id, d.err
}

func newHandler(t *testing.T, q httpapi.Queries, d httpapi.UserDeleter, opts httpapi.Options) *httpapi.Handler {
	t.Helper()
	if opts.Tokens == nil && !opts.AllowUnauthenticated {
		opts.Tokens = []string{testToken}
	}
	h, err := httpapi.NewHandler(q, d, opts)
	require.NoError(t, err)
	return h
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, http.MethodGet, target, map[string]string{"Authorization": "Bearer " + testToken})
}

func do(t *testing.T, h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
