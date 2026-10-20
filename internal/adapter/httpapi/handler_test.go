// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	assert.NotEmpty(t, body.Error.RequestID)
	assert.NotEmpty(t, body.Error.Message)
	return body.Error.Code
}

func TestNewHandler_Validation(t *testing.T) {
	t.Parallel()
	_, err := httpapi.NewHandler(nil, nil, httpapi.Options{Tokens: []string{"x"}})
	require.ErrorIs(t, err, httpapi.ErrInvalidOptions)
	_, err = httpapi.NewHandler(&fakeQueries{}, nil, httpapi.Options{})
	require.ErrorIs(t, err, httpapi.ErrInvalidOptions)
	_, err = httpapi.NewHandler(&fakeQueries{}, nil, httpapi.Options{AllowUnauthenticated: true})
	require.NoError(t, err)
}

func TestHandler_Auth(t *testing.T) {
	t.Parallel()
	cookie := func(r *http.Request) bool { return r.Header.Get("Cookie") == "session=ok" }
	tests := []struct {
		name   string
		opts   httpapi.Options
		method string
		path   string
		hdr    map[string]string
		want   int
	}{
		{
			"valid token",
			httpapi.Options{Tokens: []string{testToken}},
			"GET", "/v1/version",
			map[string]string{"Authorization": "Bearer " + testToken},
			200,
		},
		{"missing token", httpapi.Options{Tokens: []string{testToken}}, "GET", "/v1/version", nil, 401},
		{
			"wrong token",
			httpapi.Options{Tokens: []string{testToken}},
			"GET", "/v1/version",
			map[string]string{"Authorization": "Bearer wrong"},
			401,
		},
		{"unauthenticated allowed", httpapi.Options{AllowUnauthenticated: true}, "GET", "/v1/version", nil, 200},
		{
			"cookie authorises GET",
			httpapi.Options{Tokens: []string{testToken}, SessionAuth: cookie},
			"GET", "/v1/version",
			map[string]string{"Cookie": "session=ok"},
			200,
		},
		{
			"bad cookie",
			httpapi.Options{Tokens: []string{testToken}, SessionAuth: cookie},
			"GET", "/v1/version",
			map[string]string{"Cookie": "session=no"},
			401,
		},
		{
			"cookie never authorises DELETE",
			httpapi.Options{Tokens: []string{testToken}, SessionAuth: cookie},
			"DELETE", "/v1/users/u/data",
			map[string]string{"Cookie": "session=ok"},
			401,
		},
		{"unknown path still needs auth", httpapi.Options{Tokens: []string{testToken}}, "GET", "/nope", nil, 401},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(t, &fakeQueries{}, &fakeDeleter{id: "j"}, tc.opts)
			rec := do(t, h, tc.method, tc.path, tc.hdr)
			assert.Equal(t, tc.want, rec.Code)
			if tc.want == 401 {
				assert.Equal(t, "unauthorized", errorCode(t, rec))
				assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestHandler_RoutingErrors(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{})
	rec := get(t, h, "/v1/nope")
	assert.Equal(t, 404, rec.Code)
	assert.Equal(t, "not_found", errorCode(t, rec))

	rec = do(t, h, http.MethodPost, "/v1/traces", map[string]string{"Authorization": "Bearer " + testToken})
	assert.Equal(t, 405, rec.Code)
	assert.Equal(t, "GET", rec.Header().Get("Allow"))
	assert.Equal(t, "method_not_allowed", errorCode(t, rec))
}

func TestHandler_Limits(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{MaxQueryBytes: 32, MaxBodyBytes: 8})
	rec := get(t, h, "/v1/traces?name="+strings.Repeat("a", 64))
	assert.Equal(t, http.StatusRequestURITooLong, rec.Code)
	assert.Equal(t, "uri_too_long", errorCode(t, rec))

	body := strings.NewReader(strings.Repeat("x", 64))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/version", body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Equal(t, "body_too_large", errorCode(t, rec))
}

func TestHandler_RequestIDEchoed(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{})
	rec := do(t, h, http.MethodGet, "/v1/version",
		map[string]string{"Authorization": "Bearer " + testToken, "X-Request-Id": "req-7"})
	assert.Equal(t, "req-7", rec.Header().Get("X-Request-Id"))
}

func TestHandler_Version(t *testing.T) {
	t.Parallel()
	info := httpapi.VersionInfo{Version: "1.2.3", Commit: "abc", BuildDate: "2026-10-19"}
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{Version: info})
	rec := get(t, h, "/v1/version")
	var v httpapi.VersionInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, "1.2.3", v.Version)
	assert.Equal(t, "abc", v.Commit)
	assert.NotEmpty(t, v.GoVersion)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestHandler_ErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"cursor", query.ErrInvalidCursor, 400, "invalid_cursor"},
		{"limit", query.ErrInvalidLimit, 400, "invalid_limit"},
		{"range", query.ErrInvalidRange, 400, "invalid_range"},
		{"sort", query.ErrInvalidSort, 400, "invalid_sort"},
		{"usd", query.ErrInvalidUSD, 400, "invalid_parameter"},
		{"filter", query.ErrInvalidFilter, 400, "invalid_filter"},
		{"id", trace.ErrInvalidID, 400, "invalid_id"},
		{"not found", trace.ErrNotFound, 404, "not_found"},
		{"internal", errors.New("db exploded: secret path"), 500, "internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(t, &fakeQueries{err: tc.err}, nil, httpapi.Options{})
			rec := get(t, h, "/v1/traces")
			assert.Equal(t, tc.status, rec.Code)
			assert.Equal(t, tc.code, errorCode(t, rec))
			assert.NotContains(t, rec.Body.String(), "secret path")
		})
	}
}

func TestHandler_UserDeletion(t *testing.T) {
	t.Parallel()
	auth := map[string]string{"Authorization": "Bearer " + testToken}
	d := &fakeDeleter{id: "job-1"}
	h := newHandler(t, &fakeQueries{}, d, httpapi.Options{})
	rec := do(t, h, http.MethodDelete, "/v1/users/alice%40example.com/data", auth)
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.JSONEq(t, `{"job_id":"job-1"}`, rec.Body.String())
	assert.Equal(t, "alice@example.com", d.user)

	rec = do(t, h, http.MethodDelete, "/v1/users/x/data?foo=1", auth)
	assert.Equal(t, 400, rec.Code)

	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"empty":  {admin.ErrEmptyUserID, 400, "invalid_parameter"},
		"jobs":   {admin.ErrTooManyJobs, 429, "too_many_jobs"},
		"closed": {admin.ErrClosed, 503, "shutting_down"},
	} {
		failing := newHandler(t, &fakeQueries{}, &fakeDeleter{err: tc.err}, httpapi.Options{})
		res := do(t, failing, http.MethodDelete, "/v1/users/x/data", auth)
		assert.Equal(t, tc.status, res.Code, name)
		assert.Equal(t, tc.code, errorCode(t, res), name)
	}

	h = newHandler(t, &fakeQueries{}, nil, httpapi.Options{})
	rec = do(t, h, http.MethodDelete, "/v1/users/x/data", auth)
	assert.Equal(t, 404, rec.Code)
}
