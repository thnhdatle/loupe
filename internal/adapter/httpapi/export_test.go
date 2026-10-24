// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

type fakeExporter struct {
	req     query.ExportRequest
	batches [][]query.TraceDetail
	err     error
}

func (e *fakeExporter) ExportTraces(
	_ context.Context, r query.ExportRequest, fn func([]query.TraceDetail) error,
) error {
	e.req = r
	for _, b := range e.batches {
		if err := fn(b); err != nil {
			return err
		}
	}
	return e.err
}

func detail(id string) query.TraceDetail {
	return query.TraceDetail{Summary: query.TraceSummary{TraceID: id}}
}

func TestExport_StreamsOneTracePerLine(t *testing.T) {
	t.Parallel()
	e := &fakeExporter{batches: [][]query.TraceDetail{{detail("a"), detail("b")}, {detail("c")}}}
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{Exporter: e})
	rec := get(t, h, "/v1/export/traces?format=jsonl")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	assert.True(t, rec.Flushed)
	var ids []string
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		var d query.TraceDetail
		require.NoError(t, json.Unmarshal(sc.Bytes(), &d))
		assert.NotNil(t, d.Spans, "spans encode as [] not null")
		assert.Contains(t, sc.Text(), `"models":[]`)
		ids = append(ids, d.Summary.TraceID)
	}
	assert.Equal(t, []string{"a", "b", "c"}, ids)
}

func TestExport_RangeDefaultsAndParams(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		target string
		from   time.Time
		to     time.Time
		status int
		code   string
	}{
		{"default last 24h", "/v1/export/traces", now.Add(-24 * time.Hour), now, 200, ""},
		{
			"explicit", "/v1/export/traces?from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z&format=jsonl",
			time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), 200, "",
		},
		{"format csv", "/v1/export/traces?format=csv", time.Time{}, time.Time{}, 400, "invalid_parameter"},
		{"bad from", "/v1/export/traces?from=yesterday", time.Time{}, time.Time{}, 400, "invalid_parameter"},
		{
			"empty range", "/v1/export/traces?from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z",
			time.Time{},
			time.Time{},
			400, "invalid_range",
		},
		{"unknown param", "/v1/export/traces?limit=5", time.Time{}, time.Time{}, 400, "invalid_parameter"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := &fakeExporter{}
			h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{Exporter: e, Clock: clock.NewFake(now)})
			rec := get(t, h, tc.target)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			if tc.status != 200 {
				assert.Equal(t, tc.code, errorCode(t, rec))
				return
			}
			assert.Empty(t, rec.Body.String(), "no traces: empty stream")
			assert.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
			assert.True(t, tc.from.Equal(e.req.From), e.req.From)
			assert.True(t, tc.to.Equal(e.req.To), e.req.To)
		})
	}
}

func TestExport_Auth(t *testing.T) {
	t.Parallel()
	cookie := func(r *http.Request) bool { return r.Header.Get("Cookie") == "session=ok" }
	h := newHandler(t, &fakeQueries{}, nil, httpapi.Options{
		Exporter: &fakeExporter{}, Tokens: []string{testToken}, SessionAuth: cookie,
	})
	assert.Equal(t, 401, do(t, h, "GET", "/v1/export/traces", nil).Code)
	assert.Equal(t, 200, do(t, h, "GET", "/v1/export/traces", map[string]string{"Cookie": "session=ok"}).Code)
}

func TestExport_NoExporter(t *testing.T) {
	t.Parallel()
	rec := get(t, newHandler(t, &fakeQueries{}, nil, httpapi.Options{}), "/v1/export/traces")
	assert.Equal(t, 404, rec.Code)
	assert.Equal(t, "not_found", errorCode(t, rec))
}

func TestExport_ErrorBeforeFirstByteIsEnvelope(t *testing.T) {
	t.Parallel()
	e := &fakeExporter{err: errors.New("db down")}
	rec := get(t, newHandler(t, &fakeQueries{}, nil, httpapi.Options{Exporter: e}), "/v1/export/traces")
	assert.Equal(t, 500, rec.Code)
	assert.Equal(t, "internal", errorCode(t, rec))
	assert.NotContains(t, rec.Body.String(), "db down")
}

func TestExport_MidStreamErrorIsLoggedAndStops(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	e := &fakeExporter{batches: [][]query.TraceDetail{{detail("a")}}, err: errors.New("db down")}
	rec := get(t, newHandler(t, &fakeQueries{}, nil, httpapi.Options{Exporter: e, Logger: log}), "/v1/export/traces")
	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, 1, strings.Count(rec.Body.String(), "\n"))
	assert.Contains(t, logs.String(), "export aborted")
	assert.Contains(t, logs.String(), "db down")
}
