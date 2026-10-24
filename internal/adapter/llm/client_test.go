// SPDX-License-Identifier: Apache-2.0

package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/llm"
	"github.com/thnhdatle/loupe/internal/app/analyze"
)

var prompt = analyze.Prompt{System: "be brief", User: "why did it fail?"}

func newClient(t *testing.T, srv *httptest.Server, path, key string) *llm.Client {
	t.Helper()
	c, err := llm.New(llm.Options{BaseURL: srv.URL + path, Model: "qwen2.5", APIKey: key, HTTPClient: srv.Client()})
	require.NoError(t, err)
	return c
}

func TestComplete(t *testing.T) {
	t.Parallel()
	var got struct {
		path, query, auth, contentType string
		body                           map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.query = r.URL.Path, r.URL.RawQuery
		got.auth, got.contentType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		assert.Equal(t, http.MethodPost, r.Method)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&got.body))
		_, _ = io.WriteString(w,
			`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"{\"causes\":[]}"}}]}`)
	}))
	defer srv.Close()

	out, err := newClient(t, srv, "/v1?api-version=1", "sk-test").Complete(t.Context(), prompt)
	require.NoError(t, err)
	assert.JSONEq(t, `{"causes":[]}`, out)
	assert.Equal(t, "/v1/chat/completions", got.path)
	assert.Equal(t, "api-version=1", got.query)
	assert.Equal(t, "Bearer sk-test", got.auth)
	assert.Equal(t, "application/json", got.contentType)
	assert.Equal(t, map[string]any{
		"model": "qwen2.5", "stream": false,
		"messages": []any{
			map[string]any{"role": "system", "content": "be brief"},
			map[string]any{"role": "user", "content": "why did it fail?"},
		},
	}, got.body)
}

func TestComplete_NoKeyNoHeader(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	out, err := newClient(t, srv, "/v1/", "").Complete(t.Context(), prompt)
	require.NoError(t, err)
	assert.Equal(t, "ok", out)
}

func TestComplete_Failures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		body    string
		target  error
		message string
	}{
		{
			"error status", http.StatusTooManyRequests, `{"error":{"message":"Rate limit reached"}}`, llm.ErrStatus,
			"unexpected status 429: Rate limit reached",
		},
		{"plain error body", http.StatusBadGateway, "bad gateway\n", llm.ErrStatus, "502: bad gateway"},
		{"long error body", http.StatusInternalServerError, strings.Repeat("x", 500), llm.ErrStatus, "xxx…"},
		{"no choices", http.StatusOK, `{"choices":[]}`, llm.ErrNoChoice, "no choices"},
		{"too large", http.StatusOK, strings.Repeat(" ", 1<<20+1), llm.ErrTooLarge, "too large"},
		{"not json", http.StatusOK, "<html>", nil, "decode response"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, err := newClient(t, srv, "/v1", "").Complete(t.Context(), prompt)
			require.Error(t, err)
			if tc.target != nil {
				require.ErrorIs(t, err, tc.target)
			}
			assert.Contains(t, err.Error(), tc.message)
			assert.Less(t, len(err.Error()), 300)
		})
	}
}

func TestComplete_ContextDeadline(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := newClient(t, srv, "/v1", "").Complete(ctx, prompt)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestNew_Invalid(t *testing.T) {
	t.Parallel()
	for _, o := range []llm.Options{
		{BaseURL: "", Model: "m"},
		{BaseURL: "localhost:11434", Model: "m"},
		{BaseURL: "ftp://host/v1", Model: "m"},
		{BaseURL: "http://127.0.0.1:11434/v1"},
	} {
		_, err := llm.New(o)
		require.ErrorIs(t, err, llm.ErrInvalidOptions, o.BaseURL)
	}
}
