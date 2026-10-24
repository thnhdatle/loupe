// SPDX-License-Identifier: Apache-2.0

package httpmw_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

var ulidRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

func serve(t *testing.T, h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNewULID_Format(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	a, b := httpmw.NewULID(ts), httpmw.NewULID(ts)
	assert.Regexp(t, ulidRE, a)
	assert.NotEqual(t, a, b)
	assert.Equal(t, a[:10], b[:10], "same millisecond shares the timestamp prefix")
	later := httpmw.NewULID(ts.Add(time.Second))
	assert.Greater(t, later[:10], a[:10])
}

func TestValidRequestID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"simple", "abc-123_x.y", true},
		{"empty", "", false},
		{"space", "a b", false},
		{"newline", "a\nb", false},
		{"too long", strings.Repeat("a", 65), false},
		{"max length", strings.Repeat("a", 64), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, httpmw.ValidRequestID(tc.id))
		})
	}
}

func TestRequestID_AcceptsOrGenerates(t *testing.T) {
	t.Parallel()
	var seen string
	h := httpmw.Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = httpmw.RequestIDFrom(r.Context())
	}), httpmw.RequestID(clock.NewFake(time.Unix(1_800_000_000, 0))))

	rec := serve(t, h, http.MethodGet, "/", map[string]string{"X-Request-Id": "client-1"})
	assert.Equal(t, "client-1", rec.Header().Get("X-Request-Id"))
	assert.Equal(t, "client-1", seen)

	rec = serve(t, h, http.MethodGet, "/", map[string]string{"X-Request-Id": "bad id"})
	assert.Regexp(t, ulidRE, rec.Header().Get("X-Request-Id"))
	assert.Equal(t, rec.Header().Get("X-Request-Id"), seen)
}

func TestBearer_Check(t *testing.T) {
	t.Parallel()
	b := httpmw.NewBearer([]string{"secret-a", " secret-b ", ""})
	assert.True(t, b.Enabled())
	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{"first token", "Bearer secret-a", true},
		{"second token", "bearer secret-b", true},
		{"wrong", "Bearer nope", false},
		{"wrong scheme", "Basic secret-a", false},
		{"no scheme", "secret-a", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, b.Check(tc.header))
		})
	}
	none := httpmw.NewBearer(nil)
	assert.False(t, none.Enabled())
	assert.False(t, none.Check("Bearer "))
}

func TestUnauthorized_Envelope(t *testing.T) {
	t.Parallel()
	h := httpmw.Chain(http.HandlerFunc(httpmw.Unauthorized), httpmw.RequestID(clock.System{}))
	rec := serve(t, h, http.MethodGet, "/", map[string]string{"X-Request-Id": "r1"})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
	var body httpmw.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, httpmw.ErrorDetail{Code: "unauthorized", Message: "missing or invalid bearer token", RequestID: "r1"},
		body.Error)
}

func TestRecover_PanicBecomes500(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	h := httpmw.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		httpmw.Recover(log), httpmw.RequestID(clock.System{}))
	rec := serve(t, h, http.MethodGet, "/", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"internal"`)
	assert.Contains(t, logs.String(), "boom")
	assert.NotContains(t, rec.Body.String(), "boom")
}

func TestRecover_AbortHandlerRepanics(t *testing.T) {
	t.Parallel()
	h := httpmw.Recover(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	assert.PanicsWithError(t, http.ErrAbortHandler.Error(), func() { serve(t, h, http.MethodGet, "/", nil) })
}

func TestRecover_NilLoggerAndNoPanic(t *testing.T) {
	t.Parallel()
	h := httpmw.Recover(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	assert.Equal(t, http.StatusTeapot, serve(t, h, http.MethodGet, "/", nil).Code)
	p := httpmw.Recover(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("x") }))
	assert.Equal(t, http.StatusInternalServerError, serve(t, p, http.MethodGet, "/", nil).Code)
}

func TestWriteJSON_EncodeFailure(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	httpmw.WriteJSON(rec, http.StatusOK, make(chan int))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"internal"`)
}

func TestRouter_NotFoundAndMethodNotAllowed(t *testing.T) {
	t.Parallel()
	r := httpmw.NewRouter()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	r.Handle(http.MethodGet, "/v1/x/{id}", ok)
	r.Handle(http.MethodDelete, "/v1/x/{id}", ok)

	assert.Equal(t, http.StatusNoContent, serve(t, r, http.MethodGet, "/v1/x/1", nil).Code)
	rec := serve(t, r, http.MethodPost, "/v1/x/1", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "DELETE, GET", rec.Header().Get("Allow"))
	assert.Contains(t, rec.Body.String(), "method_not_allowed")
	rec = serve(t, r, http.MethodGet, "/nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not_found")
}

func TestObserve_ReportsPatternStatusAndDuration(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	r := httpmw.NewRouter()
	r.Handle(http.MethodGet, "/v1/x/{id}", func(w http.ResponseWriter, _ *http.Request) {
		clk.Advance(5 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	})
	var logs bytes.Buffer
	var gotEP string
	var gotStatus int
	var gotDur time.Duration
	obs := func(ep string, st int, d time.Duration) { gotEP, gotStatus, gotDur = ep, st, d }
	h := httpmw.Observe(clk, obs, slog.New(slog.NewTextHandler(&logs, nil)))(r)

	serve(t, h, http.MethodGet, "/v1/x/42", nil)
	assert.Equal(t, "GET /v1/x/{id}", gotEP)
	assert.Equal(t, http.StatusOK, gotStatus)
	assert.Equal(t, 5*time.Millisecond, gotDur)
	assert.Contains(t, logs.String(), "duration_ms=5")

	serve(t, h, http.MethodGet, "/nope", nil)
	assert.Equal(t, "/", gotEP)
	assert.Equal(t, http.StatusNotFound, gotStatus)
}

func TestObserve_UnmatchedAndDisabled(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	var ep string
	h := httpmw.Observe(clock.System{}, func(e string, _ int, _ time.Duration) { ep = e }, nil)(next)
	serve(t, h, http.MethodGet, "/", nil)
	assert.Equal(t, "unmatched", ep)

	off := httpmw.Observe(clock.System{}, nil, nil)(next)
	assert.Equal(t, http.StatusOK, serve(t, off, http.MethodGet, "/", nil).Code)
}

func TestNewServer_SetsTimeouts(t *testing.T) {
	t.Parallel()
	s := httpmw.NewServer("127.0.0.1:0", http.NotFoundHandler(), nil)
	assert.Equal(t, httpmw.ReadHeaderTimeout, s.ReadHeaderTimeout)
	assert.Equal(t, httpmw.ReadTimeout, s.ReadTimeout)
	assert.Equal(t, httpmw.WriteTimeout, s.WriteTimeout)
	assert.Equal(t, httpmw.IdleTimeout, s.IdleTimeout)
	assert.Equal(t, httpmw.MaxHeaderBytes, s.MaxHeaderBytes)
}

func TestExtendWriteDeadline(t *testing.T) {
	t.Parallel()
	for name, extend := range map[string]bool{"extended": true, "default": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				rec := &httpmw.StatusRecorder{ResponseWriter: w}
				if extend {
					httpmw.ExtendWriteDeadline(rec, time.Second)
				}
				time.Sleep(300 * time.Millisecond)
				_, _ = rec.Write([]byte("ok"))
			}))
			srv.Config.WriteTimeout = 100 * time.Millisecond
			srv.Start()
			defer srv.Close()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
			require.NoError(t, err)
			resp, err := srv.Client().Do(req)
			if !extend {
				if err == nil {
					_ = resp.Body.Close()
				}
				require.Error(t, err, "the server write timeout cuts the response")
				return
			}
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, "ok", string(body))
		})
	}
}

func TestStatusRecorder_ImplicitAndUnwrap(t *testing.T) {
	t.Parallel()
	inner := httptest.NewRecorder()
	rec := &httpmw.StatusRecorder{ResponseWriter: inner}
	_, err := rec.Write([]byte("x"))
	require.NoError(t, err)
	rec.WriteHeader(http.StatusTeapot)
	assert.Equal(t, http.StatusOK, rec.Status)
	assert.Same(t, inner, rec.Unwrap())
}
