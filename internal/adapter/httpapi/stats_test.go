// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestListSessions(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{}
	h := newHandler(t, q, nil, httpapi.Options{})
	rec := get(t, h, "/v1/sessions?user_id=u&from=2026-10-01T00:00:00Z&limit=5&cursor=c")
	require.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"data":[],"next_cursor":""}`, rec.Body.String())
	assert.Equal(t, "u", q.sessionReq.Filter.UserID)
	assert.Equal(t, query.Page{Limit: 5, Cursor: "c"}, q.sessionReq.Page)

	assert.Equal(t, 400, get(t, h, "/v1/sessions?bogus=1").Code)
	assert.Equal(t, 400, get(t, h, "/v1/sessions?to=nope").Code)
}

func TestGetSession(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{sessDetail: query.SessionDetail{Summary: query.SessionSummary{ID: "s"}}}
	h := newHandler(t, q, nil, httpapi.Options{})
	rec := get(t, h, "/v1/sessions/s%201?limit=2")
	require.Equal(t, 200, rec.Code)
	assert.Equal(t, "s 1", q.sessionID)
	assert.Equal(t, 2, q.sessionPg.Limit)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []any{}, got["data"])

	assert.Equal(t, 400, get(t, h, "/v1/sessions/s?x=1").Code)
	h = newHandler(t, &fakeQueries{err: trace.ErrNotFound}, nil, httpapi.Options{})
	assert.Equal(t, 404, get(t, h, "/v1/sessions/s").Code)
}

func TestOverview(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	q := &fakeQueries{overview: queries.Overview{
		From: from, To: from.Add(2 * time.Hour), GroupBy: query.GroupModel, Interval: query.IntervalHour,
		Total:  queries.Stats{Requests: 4, Errors: 1, CostNanos: 7, P95Ns: 9},
		Groups: []queries.GroupStats{{Key: "gpt", Stats: queries.Stats{Requests: 4}}, {Other: true, Users: 3}},
		Series: []queries.SeriesPoint{{Bucket: from, Key: "gpt", Stats: queries.Stats{Requests: 2}}},
	}}
	h := newHandler(t, q, nil, httpapi.Options{})
	rec := get(t, h, "/v1/stats/overview?group_by=model&interval=hour&kind=llm"+
		"&from=2026-10-01T00:00:00Z&to=2026-10-01T02:00:00Z")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Equal(t, query.OverviewRequest{
		From: from, To: from.Add(2 * time.Hour), GroupBy: query.GroupModel, Interval: query.IntervalHour, Kind: trace.KindLLM,
	}, q.overReq)

	var got struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Total  map[string]any
		Groups []map[string]any
		Series []map[string]any
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "2026-10-01T00:00:00Z", got.From)
	assert.Equal(t, "2026-10-01T02:00:00Z", got.To)
	assert.InDelta(t, 4, got.Total["requests"], 0)
	assert.InDelta(t, 9, got.Total["p95_ns"], 0)
	require.Len(t, got.Groups, 2)
	assert.Equal(t, true, got.Groups[1]["other"])
	assert.Equal(t, "2026-10-01T00:00:00Z", got.Series[0]["bucket"])

	assert.Equal(t, 400, get(t, h, "/v1/stats/overview?x=1").Code)
	assert.Equal(t, 400, get(t, h, "/v1/stats/overview?from=x").Code)
	empty := newHandler(t, &fakeQueries{}, nil, httpapi.Options{})
	assert.Contains(t, get(t, empty, "/v1/stats/overview").Body.String(), `"groups":[],"series":[]`)
}

func TestTopSpans(t *testing.T) {
	t.Parallel()
	q := &fakeQueries{top: []query.TopSpan{{TraceID: "t"}}}
	h := newHandler(t, q, nil, httpapi.Options{})
	rec := get(t, h, "/v1/stats/top-spans?by=duration&limit=3")
	require.Equal(t, 200, rec.Code)
	assert.Equal(t, query.TopByDuration, q.topReq.By)
	assert.Equal(t, 3, q.topReq.Limit)
	assert.Contains(t, rec.Body.String(), `"trace_id":"t"`)

	assert.Equal(t, 400, get(t, h, "/v1/stats/top-spans?y=1").Code)
	assert.Equal(t, 400, get(t, h, "/v1/stats/top-spans?to=x").Code)
	empty := newHandler(t, &fakeQueries{}, nil, httpapi.Options{})
	assert.JSONEq(t, `{"data":[]}`, get(t, empty, "/v1/stats/top-spans").Body.String())
}

func TestIngestStats(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{ingest: query.IngestStats{Received: 5, Rejected: map[string]int64{"queue_full": 2}}},
		nil, httpapi.Options{})
	rec := get(t, h, "/v1/ingest/stats")
	require.Equal(t, 200, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.InDelta(t, 5, got["received"], 0)
	assert.Equal(t, map[string]any{"queue_full": float64(2)}, got["rejected"])
	assert.Equal(t, map[string]any{}, got["dropped"])
	assert.Equal(t, map[string]any{}, got["discarded"])

	assert.Equal(t, 400, get(t, h, "/v1/ingest/stats?x=1").Code)
}

func TestInternalErrorsAreLoggedNotExposed(t *testing.T) {
	t.Parallel()
	h := newHandler(t, &fakeQueries{err: errors.New("boom")}, nil, httpapi.Options{})
	for _, p := range []string{"/v1/sessions", "/v1/stats/overview", "/v1/stats/top-spans", "/v1/ingest/stats"} {
		rec := get(t, h, p)
		assert.Equal(t, 500, rec.Code, p)
		assert.NotContains(t, rec.Body.String(), "boom", p)
	}
}
