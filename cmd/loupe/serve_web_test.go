// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/adminhttp"
	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const dashToken = "dash-test-token"

type failingAdmin struct{ t *testing.T }

func (f failingAdmin) Recompute(*cost.Calculator, admin.Range) (string, error) {
	f.t.Error("recompute reached")
	return "", nil
}

func (f failingAdmin) Vacuum() (string, error) {
	f.t.Error("vacuum reached")
	return "", nil
}

func (f failingAdmin) Backup(string) (string, error) {
	f.t.Error("backup reached")
	return "", nil
}

func (f failingAdmin) JobStatus(string) (admin.JobStatus, error) {
	f.t.Error("job status reached")
	return admin.JobStatus{}, nil
}

type failingDeleter struct{ t *testing.T }

func (f failingDeleter) DeleteUser(string) (string, error) {
	f.t.Error("delete user reached")
	return "", nil
}

func TestDashboard_SessionCookieScope(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.UI.Listen = "0.0.0.0:3000"
	var logs bytes.Buffer
	s := &server{cfg: &cfg, clk: clock.System{}, log: slog.New(slog.NewTextHandler(&logs, nil))}
	q := queries.New(queries.Readers{}, s.clk)
	dash, err := s.newDashboard(q, []string{dashToken}, nil)
	require.NoError(t, err)
	api, err := httpapi.NewHandler(q, failingDeleter{t}, httpapi.Options{
		Tokens: []string{dashToken}, SessionAuth: dash.SessionAuth,
	})
	require.NoError(t, err)
	adm, err := adminhttp.NewHandler(failingAdmin{t}, adminhttp.Options{
		Tokens: []string{dashToken}, StorageDir: t.TempDir(),
		Calculator: func() (*cost.Calculator, error) { return nil, nil }, //nolint:nilnil // never called.
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/v1/", api)
	mux.Handle("/v1/admin/", adm)
	mux.Handle("/", dash)

	do := func(method, target string, cookie *http.Cookie, body string) int {
		req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	rec := httptest.NewRecorder()
	login := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/login",
		strings.NewReader(url.Values{"token": {dashToken}}.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, login)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]

	assert.Equal(t, http.StatusUnauthorized, do(http.MethodGet, "/v1/version", nil, ""), "no credentials")
	assert.Equal(t, http.StatusOK, do(http.MethodGet, "/v1/version", c, ""), "cookie reads GET /v1/*")
	assert.Equal(t, http.StatusUnauthorized, do(http.MethodDelete, "/v1/users/u1/data", c, ""), "cookie cannot delete")
	assert.Equal(t, http.StatusUnauthorized, do(http.MethodPost, "/v1/traces/"+strings.Repeat("ab", 16)+"/analyze", c, ""),
		"cookie cannot analyze through the API")
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/v1/admin/vacuum"},
		{http.MethodPost, "/v1/admin/backups"},
		{http.MethodPost, "/v1/admin/pricing/recompute"},
		{http.MethodGet, "/v1/admin/jobs/x"},
	} {
		assert.Equal(t, http.StatusUnauthorized, do(route.method, route.path, c, "{}"), route.method+" "+route.path)
	}
	assert.Equal(t, http.StatusSeeOther, do(http.MethodGet, "/", nil, ""), "non-loopback pages need a session")
	assert.Equal(t, http.StatusOK, do(http.MethodGet, "/static/app.css", nil, ""))
}

func TestDashboard_LoginURLLog(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		generate bool
		listen   string
		tls      bool
		wantURL  string
	}{
		{"unspecified host", true, "0.0.0.0:3000", false, "http://localhost:3000/login?code="},
		{"loopback", true, "127.0.0.1:3100", false, "http://127.0.0.1:3100/login?code="},
		{"tls", true, "0.0.0.0:3000", true, "https://localhost:3000/login?code="},
		{"off", false, "0.0.0.0:3000", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.UI.Listen, cfg.API.GenerateToken = tc.listen, tc.generate
			if tc.tls {
				cfg.UI.TLS = config.TLS{CertFile: "c", KeyFile: "k"}
			}
			var logs bytes.Buffer
			s := &server{cfg: &cfg, clk: clock.System{}, log: slog.New(slog.NewTextHandler(&logs, nil))}
			dash, err := s.newDashboard(queries.New(queries.Readers{}, s.clk), []string{dashToken}, nil)
			require.NoError(t, err)
			out := logs.String()
			assert.NotContains(t, out, dashToken, "the API token is never logged")
			if tc.wantURL == "" {
				assert.NotContains(t, out, "/login?code=")
				return
			}
			m := regexp.MustCompile(regexp.QuoteMeta(tc.wantURL) + `([A-Za-z0-9_-]{43})`).FindStringSubmatch(out)
			require.Len(t, m, 2, out)
			assert.Contains(t, out, "component=web")

			rec := httptest.NewRecorder()
			dash.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
				"/login?code="+m[1], http.NoBody))
			assert.Equal(t, http.StatusSeeOther, rec.Code, "the logged code logs in")
			assert.Equal(t, tc.tls, rec.Result().Cookies()[0].Secure)
		})
	}
}
