// SPDX-License-Identifier: Apache-2.0

package adminhttp_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/adminhttp"
	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const testToken = "adm1n"

type fakeAdmin struct {
	calc   *cost.Calculator
	rng    admin.Range
	path   string
	vacuum int
	status admin.JobStatus
	err    error
}

func (f *fakeAdmin) Recompute(c *cost.Calculator, r admin.Range) (string, error) {
	f.calc, f.rng = c, r
	return "job-r", f.err
}

func (f *fakeAdmin) Vacuum() (string, error) {
	f.vacuum++
	return "job-v", f.err
}

func (f *fakeAdmin) Backup(path string) (string, error) {
	f.path = path
	return "job-b", f.err
}

func (f *fakeAdmin) JobStatus(string) (admin.JobStatus, error) { return f.status, f.err }

func opts() adminhttp.Options {
	return adminhttp.Options{
		Tokens:     []string{testToken},
		StorageDir: "/data",
		Calculator: func() (*cost.Calculator, error) { return &cost.Calculator{}, nil },
		Clock:      clock.NewFake(time.Date(2026, 10, 19, 8, 30, 15, 0, time.UTC)),
	}
}

func newHandler(t *testing.T, f *fakeAdmin, o adminhttp.Options) *adminhttp.Handler {
	t.Helper()
	h, err := adminhttp.NewHandler(f, o)
	require.NoError(t, err)
	return h
}

// do sends an authenticated request with body.
func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// doAnon sends a request without credentials.
func doAnon(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b), rec.Body.String())
	assert.NotEmpty(t, b.Error.RequestID)
	return b.Error.Code
}

func TestNewHandler_Validation(t *testing.T) {
	t.Parallel()
	_, err := adminhttp.NewHandler(nil, opts())
	require.ErrorIs(t, err, adminhttp.ErrInvalidOptions)

	o := opts()
	o.Tokens = nil
	_, err = adminhttp.NewHandler(&fakeAdmin{}, o)
	require.ErrorIs(t, err, adminhttp.ErrInvalidOptions)
	o.AllowUnauthenticated = true
	_, err = adminhttp.NewHandler(&fakeAdmin{}, o)
	require.NoError(t, err)

	o = opts()
	o.StorageDir = ""
	_, err = adminhttp.NewHandler(&fakeAdmin{}, o)
	require.ErrorIs(t, err, adminhttp.ErrInvalidOptions)
	o = opts()
	o.Calculator = nil
	_, err = adminhttp.NewHandler(&fakeAdmin{}, o)
	require.ErrorIs(t, err, adminhttp.ErrInvalidOptions)
}

func TestHandler_RequiresToken(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeAdmin{}, opts())
	for _, r := range [][2]string{
		{"POST", "/v1/admin/vacuum"},
		{"POST", "/v1/admin/backups"},
		{"POST", "/v1/admin/pricing/recompute"},
		{"GET", "/v1/admin/jobs/x"},
		{"GET", "/v1/other"},
	} {
		rec := doAnon(t, h, r[0], r[1])
		assert.Equal(t, 401, rec.Code, r[1])
		assert.Equal(t, "unauthorized", code(t, rec))
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/admin/vacuum", nil)
	req.Header.Set("Authorization", "Bearer nope")
	req.AddCookie(&http.Cookie{Name: "session", Value: "ok"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, 401, rec.Code, "a session cookie never authorises admin calls")
}

func TestHandler_UnauthenticatedAllowed(t *testing.T) {
	t.Parallel()
	o := opts()
	o.Tokens = nil
	o.AllowUnauthenticated = true
	f := &fakeAdmin{}
	rec := doAnon(t, newHandler(t, f, o), http.MethodPost, "/v1/admin/vacuum")
	assert.Equal(t, http.StatusAccepted, rec.Code)
}

func TestHandler_Vacuum(t *testing.T) {
	t.Parallel()
	f := &fakeAdmin{}
	rec := do(t, newHandler(t, f, opts()), http.MethodPost, "/v1/admin/vacuum", "")
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.JSONEq(t, `{"job_id":"job-v"}`, rec.Body.String())
	assert.Equal(t, 1, f.vacuum)
}

func TestHandler_Backup(t *testing.T) {
	t.Parallel()
	f := &fakeAdmin{}
	rec := do(t, newHandler(t, f, opts()), http.MethodPost, "/v1/admin/backups", "")
	assert.Equal(t, http.StatusAccepted, rec.Code)
	want := filepath.Join("/data", "backups", "loupe-20261019T083015Z.db")
	assert.Equal(t, want, f.path)
	assert.JSONEq(t, `{"job_id":"job-b","path":"`+want+`"}`, rec.Body.String())
}

func TestHandler_Recompute(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		body  string
		since time.Time
		until time.Time
		want  int
	}{
		{"empty body", "", time.Time{}, time.Time{}, 202},
		{"empty object", "{}", time.Time{}, time.Time{}, 202},
		{
			"both", `{"since":"2026-10-01T00:00:00Z","until":"2026-10-02T00:00:00Z"}`,
			time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), 202,
		},
		{"bad time", `{"since":"yesterday"}`, time.Time{}, time.Time{}, 400},
		{"unknown field", `{"from":"2026-10-01T00:00:00Z"}`, time.Time{}, time.Time{}, 400},
		{"not json", `nope`, time.Time{}, time.Time{}, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeAdmin{}
			rec := do(t, newHandler(t, f, opts()), http.MethodPost, "/v1/admin/pricing/recompute", tc.body)
			assert.Equal(t, tc.want, rec.Code, rec.Body.String())
			if tc.want == 202 {
				assert.JSONEq(t, `{"job_id":"job-r"}`, rec.Body.String())
				assert.NotNil(t, f.calc)
				assert.True(t, f.rng.Since.Equal(tc.since))
				assert.True(t, f.rng.Until.Equal(tc.until))
			} else {
				assert.Equal(t, "invalid_parameter", code(t, rec))
			}
		})
	}
}

func TestHandler_RecomputeBodyTooLarge(t *testing.T) {
	t.Parallel()
	o := opts()
	o.MaxBodyBytes = 16
	h := newHandler(t, &fakeAdmin{}, o)
	// Content-Length known: rejected before the handler.
	rec := do(t, h, http.MethodPost, "/v1/admin/pricing/recompute", strings.Repeat(" ", 64))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Equal(t, "body_too_large", code(t, rec))

	// Unknown length: caught by the body reader.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/admin/pricing/recompute",
		strings.NewReader(`{"since":"`+strings.Repeat("a", 64)+`"}`))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestHandler_RecomputeCalculatorFailure(t *testing.T) {
	t.Parallel()
	o := opts()
	o.Calculator = func() (*cost.Calculator, error) { return nil, errors.New("price table path /etc/x") }
	rec := do(t, newHandler(t, &fakeAdmin{}, o), http.MethodPost, "/v1/admin/pricing/recompute", "")
	assert.Equal(t, 500, rec.Code)
	assert.NotContains(t, rec.Body.String(), "/etc/x")
}

func TestHandler_JobStatus(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	f := &fakeAdmin{status: admin.JobStatus{
		ID: "j1", Kind: admin.KindVacuum, State: admin.StateDone, Rows: 12,
		CreatedAt: created, StartedAt: created.Add(time.Second),
	}}
	rec := do(t, newHandler(t, f, opts()), http.MethodGet, "/v1/admin/jobs/j1", "")
	require.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"job_id":"j1","kind":"vacuum","state":"done","rows":12,
		"created_at":"2026-10-19T08:00:00Z","started_at":"2026-10-19T08:00:01Z","finished_at":null}`, rec.Body.String())
}

func TestHandler_ErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"job not found", admin.ErrJobNotFound, 404, "not_found"},
		{"range", admin.ErrInvalidRange, 400, "invalid_range"},
		{"too many", admin.ErrTooManyJobs, 429, "too_many_jobs"},
		{"closed", admin.ErrClosed, 503, "shutting_down"},
		{"other", errors.New("disk on fire"), 500, "internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(t, &fakeAdmin{err: tc.err}, opts())
			for _, r := range [][2]string{{"POST", "/v1/admin/vacuum"}, {"GET", "/v1/admin/jobs/x"}} {
				rec := do(t, h, r[0], r[1], "")
				assert.Equal(t, tc.status, rec.Code)
				assert.Equal(t, tc.code, code(t, rec))
				assert.NotContains(t, rec.Body.String(), "disk on fire")
			}
		})
	}
}

func TestHandler_Routing(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeAdmin{}, opts())
	rec := do(t, h, http.MethodGet, "/v1/admin/vacuum", "")
	assert.Equal(t, 405, rec.Code)
	assert.Equal(t, "POST", rec.Header().Get("Allow"))
	assert.Equal(t, 404, do(t, h, http.MethodGet, "/v1/admin/nope", "").Code)
}
