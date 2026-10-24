// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/adminhttp"
	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/testutil/openapispec"
)

type nopAdmin struct{}

func (nopAdmin) Recompute(*cost.Calculator, admin.Range) (string, error) { return "", nil }
func (nopAdmin) Vacuum() (string, error)                                 { return "", nil }
func (nopAdmin) Backup(string) (string, error)                           { return "", nil }
func (nopAdmin) JobStatus(string) (admin.JobStatus, error)               { return admin.JobStatus{}, nil }

func TestOpenAPI_RoutesMatchSpec(t *testing.T) {
	t.Parallel()
	api := newHandler(t, &fakeQueries{}, &fakeDeleter{}, httpapi.Options{})
	adm, err := adminhttp.NewHandler(nopAdmin{}, adminhttp.Options{
		Tokens: []string{"x"}, StorageDir: "/data",
		Calculator: func() (*cost.Calculator, error) { return &cost.Calculator{}, nil },
	})
	require.NoError(t, err)
	registered := append(api.Routes(), adm.Routes()...)
	documented := make([]string, 0, len(registered))
	for _, op := range openapispec.Load(t).Operations() {
		documented = append(documented, op.Route())
	}
	slices.Sort(registered)
	slices.Sort(documented)
	assert.Equal(t, registered, documented)
}

func TestOpenAPI_OperationsComplete(t *testing.T) {
	t.Parallel()
	for _, op := range openapispec.Load(t).Operations() {
		t.Run(op.Route(), func(t *testing.T) {
			t.Parallel()
			assert.NotEmpty(t, op.ID)
			var okEx, errEx bool
			for _, st := range op.Statuses() {
				_, ex := op.Response(st)
				assert.NotNil(t, ex, "response %s has no example", st)
				if strings.HasPrefix(st, "2") {
					okEx = true
				} else if ex != nil {
					errEx = true
					m, _ := ex.(map[string]any)
					e, _ := m["error"].(map[string]any)
					assert.Subset(t, keys(e), []string{"code", "message", "request_id"}, st)
				}
			}
			assert.True(t, okEx, "no 2xx example")
			assert.True(t, errEx, "no error example")
		})
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func decodeExample[T any](t *testing.T, ex any) T {
	t.Helper()
	b, err := json.Marshal(ex)
	require.NoError(t, err)
	var v T
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&v))
	return v
}

type exampleExporter struct{ batch []query.TraceDetail }

func (e exampleExporter) ExportTraces(
	_ context.Context, _ query.ExportRequest, fn func([]query.TraceDetail) error,
) error {
	return fn(e.batch)
}

type contractCase struct {
	setup     func(t *testing.T, ex any, f *fakeQueries, d *fakeDeleter, o *httpapi.Options)
	shapeOnly bool
}

func contractCases() map[string]contractCase {
	return map[string]contractCase{
		"listTraces": {setup: func(t *testing.T, ex any, f *fakeQueries, _ *fakeDeleter, _ *httpapi.Options) {
			t.Helper()
			f.tracePage = decodeExample[query.TracePage](t, ex)
		}},
		"getTrace": {setup: func(t *testing.T, ex any, f *fakeQueries, _ *fakeDeleter, _ *httpapi.Options) {
			t.Helper()
			f.detail = decodeExample[query.TraceDetail](t, ex)
		}},
		"listSessions": {setup: func(t *testing.T, ex any, f *fakeQueries, _ *fakeDeleter, _ *httpapi.Options) {
			t.Helper()
			f.sessPage = decodeExample[query.SessionPage](t, ex)
		}},
		"getSession": {setup: func(t *testing.T, ex any, f *fakeQueries, _ *fakeDeleter, _ *httpapi.Options) {
			t.Helper()
			f.sessDetail = decodeExample[query.SessionDetail](t, ex)
		}},
		"statsTopSpans": {setup: func(t *testing.T, ex any, f *fakeQueries, _ *fakeDeleter, _ *httpapi.Options) {
			t.Helper()
			f.top = decodeExample[struct {
				Data []query.TopSpan `json:"data"`
			}](t, ex).Data
		}},
		"ingestStats": {setup: func(t *testing.T, ex any, f *fakeQueries, _ *fakeDeleter, _ *httpapi.Options) {
			t.Helper()
			f.ingest = decodeExample[query.IngestStats](t, ex)
		}},
		"getVersion": {setup: func(t *testing.T, ex any, _ *fakeQueries, _ *fakeDeleter, o *httpapi.Options) {
			t.Helper()
			o.Version = decodeExample[httpapi.VersionInfo](t, ex)
		}},
		"deleteUserData": {setup: func(t *testing.T, ex any, _ *fakeQueries, d *fakeDeleter, _ *httpapi.Options) {
			t.Helper()
			d.id = decodeExample[map[string]string](t, ex)["job_id"]
		}},
		"exportTraces": {setup: func(t *testing.T, ex any, _ *fakeQueries, _ *fakeDeleter, o *httpapi.Options) {
			t.Helper()
			o.Exporter = exampleExporter{batch: []query.TraceDetail{decodeExample[query.TraceDetail](t, ex)}}
		}},
		"statsOverview": {shapeOnly: true, setup: func(_ *testing.T, _ any, f *fakeQueries, _ *fakeDeleter,
			_ *httpapi.Options,
		) {
			f.overview = queries.Overview{
				Groups: []queries.GroupStats{{Key: "gpt-4o"}},
				Series: []queries.SeriesPoint{{Key: "gpt-4o"}},
			}
		}},
	}
}

func TestOpenAPI_Contract(t *testing.T) {
	t.Parallel()
	cases := contractCases()
	for _, op := range openapispec.Load(t).Operations() {
		if strings.HasPrefix(op.Path, "/v1/admin/") {
			continue
		}
		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()
			tc, ok := cases[op.ID]
			require.True(t, ok, "no contract case for %s", op.ID)
			var status string
			for _, st := range op.Statuses() {
				if strings.HasPrefix(st, "2") {
					status = st
				}
			}
			mt, ex := op.Response(status)
			require.NotNil(t, ex)
			f, d, o := &fakeQueries{}, &fakeDeleter{}, httpapi.Options{}
			tc.setup(t, ex, f, d, &o)
			rec := do(t, newHandler(t, f, d, o), op.Method, op.Target(),
				map[string]string{"Authorization": "Bearer " + testToken})
			require.Equal(t, atoi(status), rec.Code, rec.Body.String())
			assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), mt), rec.Header().Get("Content-Type"))
			var got any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got), rec.Body.String())
			if tc.shapeOnly {
				assert.Empty(t, openapispec.SameShape("$", ex, got))
				return
			}
			want, err := json.Marshal(ex)
			require.NoError(t, err)
			assert.JSONEq(t, string(want), rec.Body.String())
		})
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestOpenAPI_ErrorExamples(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{Exporter: &exampleExporter{}})
	tests := []struct {
		name   string
		target string
		hdr    map[string]string
		status int
		code   string
	}{
		{"unauthorized", "/v1/traces", nil, http.StatusUnauthorized, "unauthorized"},
		{
			"unknown parameter", "/v1/traces?foo=1",
			map[string]string{"Authorization": "Bearer " + testToken},
			http.StatusBadRequest, "invalid_parameter",
		},
		{
			"bad export format", "/v1/export/traces?format=csv",
			map[string]string{"Authorization": "Bearer " + testToken},
			http.StatusBadRequest, "invalid_parameter",
		},
		{
			"unknown endpoint", "/v1/nope",
			map[string]string{"Authorization": "Bearer " + testToken},
			http.StatusNotFound, "not_found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := do(t, h, http.MethodGet, tc.target, tc.hdr)
			assert.Equal(t, tc.status, rec.Code)
			assert.Equal(t, tc.code, errorCode(t, rec))
		})
	}
}
