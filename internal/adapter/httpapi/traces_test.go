// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestListTraces_ParsesFilters(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{tracePage: query.TracePage{
		Data:       []query.TraceSummary{{TraceID: "t1", Usage: trace.Usage{Input: 3}}},
		NextCursor: "next",
	}}
	h := newHandler(t, q, nil, httpapi.Options{})
	v := url.Values{
		"from": {"2026-10-01T00:00:00Z"}, "to": {"2026-10-02T00:00:00+02:00"},
		"session_id": {"s1"}, "user_id": {"u1"}, "min_cost": {"0.25"}, "has_error": {"true"},
		"service": {"svc"}, "model": {"m"}, "provider": {"p"}, "kind": {"llm"}, "status": {"ERROR"},
		"min_duration_ms": {"1500"}, "name": {"chat "}, "sort": {"cost"}, "order": {"asc"},
		"limit": {"10"}, "cursor": {"abc"},
	}
	rec := get(t, h, "/v1/traces?"+v.Encode())
	require.Equal(t, 200, rec.Code, rec.Body.String())

	r := q.traceReq
	assert.True(t, r.Filter.From.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)))
	assert.True(t, r.Filter.To.Equal(time.Date(2026, 10, 1, 22, 0, 0, 0, time.UTC)))
	assert.Equal(t, "s1", r.Filter.SessionID)
	assert.Equal(t, "u1", r.Filter.UserID)
	require.NotNil(t, r.Filter.MinCostNanos)
	assert.Equal(t, int64(250_000_000), *r.Filter.MinCostNanos)
	require.NotNil(t, r.Filter.HasError)
	assert.True(t, *r.Filter.HasError)
	assert.Equal(t, query.SpanFilter{
		Service: "svc", Model: "m", Provider: "p", Kind: trace.KindLLM, Status: trace.StatusError,
		MinDurationNs: 1_500_000_000, NamePrefix: "chat ",
	}, r.Filter.Span)
	assert.Equal(t, query.SortCost, r.Sort)
	assert.Equal(t, query.OrderAsc, r.Order)
	assert.Equal(t, query.Page{Limit: 10, Cursor: "abc"}, r.Page)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "next", got["next_cursor"])
	var typed struct {
		Data []struct {
			Models []string `json:"models"`
			Usage  struct {
				Input int `json:"input"`
			} `json:"usage"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &typed))
	require.Len(t, typed.Data, 1)
	assert.NotNil(t, typed.Data[0].Models, "nil slices encode as []")
	assert.Equal(t, 3, typed.Data[0].Usage.Input)
}

func TestListTraces_EmptyPageIsArray(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{})
	rec := get(t, h, "/v1/traces")
	assert.JSONEq(t, `{"data":[],"next_cursor":""}`, rec.Body.String())
}

func TestListTraces_BadParameters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
		code  string
	}{
		{"unknown param", "foo=1", "invalid_parameter"},
		{"repeated param", "model=a&model=b", "invalid_parameter"},
		{"bad time", "from=yesterday", "invalid_parameter"},
		{"bad bool", "has_error=maybe", "invalid_parameter"},
		{"bad min_cost", "min_cost=abc", "invalid_parameter"},
		{"negative min_cost", "min_cost=-1", "invalid_parameter"},
		{"bad duration", "min_duration_ms=-5", "invalid_parameter"},
		{"duration overflow", "min_duration_ms=9223372036854775807", "invalid_parameter"},
		{"non-numeric limit", "limit=ten", "invalid_limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := &fakeQueries{}
			h := newHandler(t, q, nil, httpapi.Options{})
			rec := get(t, h, "/v1/traces?"+tc.query)
			if tc.code == "invalid_limit" {
				// Rejected by the use case; the fake lets it through, so check the forwarded value.
				assert.Equal(t, -1, q.traceReq.Page.Limit)
				return
			}
			assert.Equal(t, 400, rec.Code)
			assert.Equal(t, tc.code, errorCode(t, rec))
		})
	}
}

func TestGetTrace(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{detail: query.TraceDetail{Summary: query.TraceSummary{TraceID: "abc"}}}
	h := newHandler(t, q, nil, httpapi.Options{})
	rec := get(t, h, "/v1/traces/0123?include_content=true")
	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "0123", q.traceID)
	assert.True(t, q.traceInc)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []any{}, got["spans"])

	get(t, h, "/v1/traces/0123")
	assert.False(t, q.traceInc)

	assert.Equal(t, 400, get(t, h, "/v1/traces/0123?include_content=x").Code)
	assert.Equal(t, 400, get(t, h, "/v1/traces/0123?other=1").Code)
}

func TestGetTrace_NotFound(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{err: trace.ErrNotFound}, nil, httpapi.Options{})
	rec := get(t, h, "/v1/traces/0123")
	assert.Equal(t, 404, rec.Code)
	assert.Equal(t, "not_found", errorCode(t, rec))
}
