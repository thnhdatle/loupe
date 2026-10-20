// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

func TestFormatters(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, got, want string }{
		{"money zero", formatMoney(0), "$0.00"},
		{"money dollars", formatMoney(1_234_000_000), "$1.23"},
		{"money cents", formatMoney(25_000_000), "$0.0250"},
		{"money tiny", formatMoney(1500), "$0.000002"},
		{"money nil", formatMoneyPtr(nil), "unpriced"},
		{"money ptr", formatMoneyPtr(i64p(1e9)), "$1.00"},
		{"dur ns", formatDuration(512), "512 ns"},
		{"dur us", formatDuration(45_000), "45 µs"},
		{"dur ms small", formatDuration(2_500_000), "2.5 ms"},
		{"dur ms", formatDuration(812_000_000), "812 ms"},
		{"dur s", formatDuration(1_240_000_000), "1.24 s"},
		{"dur min", formatDuration(125_000_000_000), "2m05s"},
		{"int small", formatInt(12), "12"},
		{"int grouped", formatInt(1234567), "1,234,567"},
		{"int negative", formatInt(-1234), "-1,234"},
		{"pct zero total", formatPercent(1, 0), "0%"},
		{"pct", formatPercent(1, 8), "12.5%"},
		{"time", formatTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano()), "2026-01-02 03:04:05"},
		{"badge", kindBadge(trace.KindRetrieval), "RET"},
		{"badge unknown", kindBadge("zzz"), "OTH"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, tc.got, tc.name)
	}
	assert.EqualValues(t, 15, totalTokens(trace.Usage{Input: 10, Output: 5, Reasoning: 3}))
}

func i64p(v int64) *int64 { return &v }

func TestResolveRange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		query    string
		wantKey  string
		wantSpan time.Duration
		wantInt  query.Interval
		wantErr  bool
	}{
		{"default", "", "24h", 24 * time.Hour, query.IntervalHour, false},
		{"unknown preset", "range=9y", "24h", 24 * time.Hour, query.IntervalHour, false},
		{"hour", "range=1h", "1h", time.Hour, query.IntervalHour, false},
		{"week", "range=7d", "7d", 7 * 24 * time.Hour, query.IntervalHour, false},
		{"month", "range=30d", "30d", 30 * 24 * time.Hour, query.IntervalDay, false},
		{"custom", "from=2026-10-19T10:00&to=2026-10-19T11:30", "custom", 90 * time.Minute, query.IntervalHour, false},
		{
			"custom rfc3339", "from=2026-10-19T10:00:00%2B02:00&to=2026-10-19T10:00:00Z", "custom", 2 * time.Hour,
			query.IntervalHour, false,
		},
		{"only from", "from=2026-10-19T00:00", "custom", 12 * time.Hour, query.IntervalHour, false},
		{"only to", "to=2026-10-19T12:00", "custom", 24 * time.Hour, query.IntervalHour, false},
		{"long custom", "from=2026-09-01T00:00&to=2026-10-01T00:00", "custom", 30 * 24 * time.Hour, query.IntervalDay, false},
		{"reversed", "from=2026-10-19T11:00&to=2026-10-19T10:00", "", 0, "", true},
		{"bad from", "from=x", "", 0, "", true},
		{"bad to", "from=2026-10-19T10:00&to=y", "", 0, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vals, err := parseQuery(tc.query)
			require.NoError(t, err)
			rv, err := resolveRange(vals, now)
			if tc.wantErr {
				require.ErrorIs(t, err, query.ErrInvalidRange)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantKey, rv.Key)
			assert.Equal(t, tc.wantSpan, rv.To.Sub(rv.From))
			assert.Equal(t, tc.wantInt, rv.Interval)
			assert.Len(t, rv.Presets, len(presets))
		})
	}
}

func TestOrderSpans_CyclesAndOrphans(t *testing.T) {
	t.Parallel()
	spans := []query.SpanView{
		{SpanID: "c", ParentSpanID: "b", StartNs: 3},
		{SpanID: "a", ParentSpanID: "c", StartNs: 1}, // a -> c -> b -> a: a cycle
		{SpanID: "b", ParentSpanID: "a", StartNs: 2},
		{SpanID: "o", ParentSpanID: "missing", StartNs: 0},
		{SpanID: "self", ParentSpanID: "self", StartNs: 9},
		{SpanID: "r", StartNs: 5},
		{SpanID: "k", ParentSpanID: "r", StartNs: 6},
	}
	order := orderSpans(spans)
	require.Len(t, order, len(spans))
	seen := map[int]bool{}
	for _, i := range order {
		seen[i] = true
	}
	assert.Len(t, seen, len(spans), "every span appears once")
	d := query.TraceDetail{Summary: query.TraceSummary{TraceID: "t"}, Spans: spans}
	rows := waterfall(d, order, 0)
	assert.Len(t, rows, len(spans))
	for _, r := range rows {
		assert.LessOrEqual(t, r.Depth, maxDepthClass)
		assert.LessOrEqual(t, r.X+r.W, waterScale)
	}
}

func TestWaterfall_DeepTreeCapsDepth(t *testing.T) {
	t.Parallel()
	spans := make([]query.SpanView, 0, 30)
	for i := range 30 {
		s := query.SpanView{SpanID: fmt.Sprint("s", i), StartNs: int64(i), EndNs: int64(i) + 1, DurationNs: 1}
		if i > 0 {
			s.ParentSpanID = fmt.Sprint("s", i-1)
		}
		spans = append(spans, s)
	}
	d := query.TraceDetail{Spans: spans}
	rows := waterfall(d, orderSpans(spans), 0)
	assert.Equal(t, maxDepthClass, rows[29].Depth)
	assert.Equal(t, 1, rows[1].Depth)
}

func TestAttrRows(t *testing.T) {
	t.Parallel()
	assert.Nil(t, attrRows(nil))
	assert.Nil(t, attrRows(json.RawMessage(`not json`)))
	assert.Empty(t, attrRows(json.RawMessage(`null`)))
	rows := attrRows(json.RawMessage(`{"b":1,"a":"x","c":{"k":[1,2]},"d":true}`))
	assert.Equal(t, []labelValue{{"a", "x"}, {"b", "1"}, {"c", `{"k":[1,2]}`}, {"d", "true"}}, rows)
	long := attrText(strings.Repeat("é", maxAttrChars+10))
	assert.Len(t, []rune(long), maxAttrChars+1)
	assert.Empty(t, attrText(make(chan int)))
}

func TestExpiringSet_Bounded(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(1000, 0))
	s := newExpiringSet(clk)
	for i := range maxEntries {
		s.add(fmt.Sprint("k", i), time.Duration(i+1)*time.Minute)
	}
	assert.Len(t, s.m, maxEntries)
	s.add("new", time.Hour) // full, none expired: evicts the one closest to expiry (k0)
	assert.Len(t, s.m, maxEntries)
	assert.False(t, s.has("k0"))
	assert.True(t, s.has("k1"))
	assert.True(t, s.has("new"))
	clk.Advance(30 * time.Minute) // k1..k29 expire
	s.add("newer", time.Hour)
	assert.LessOrEqual(t, len(s.m), maxEntries-28)
	assert.True(t, s.take("newer"))
	assert.False(t, s.take("newer"))
	s.remove("new")
	assert.False(t, s.has("new"))
}

func TestLoginLimiter_Bounded(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(1000, 0))
	l := newLoginLimiter(clk)
	for i := range maxLimiterIPs {
		l.fail(fmt.Sprint("ip", i))
	}
	assert.Len(t, l.m, maxLimiterIPs)
	l.fail("another")
	assert.Len(t, l.m, maxLimiterIPs, "an arbitrary entry made room")
	clk.Advance(2 * loginFailWindow)
	l.fail("fresh")
	assert.Len(t, l.m, 1, "expired windows are dropped when the table is full")
	_, blocked := l.blocked("fresh")
	assert.False(t, blocked)
}

func TestParsePages_Errors(t *testing.T) {
	t.Parallel()
	_, err := parsePages(fstest.MapFS{})
	require.ErrorIs(t, err, ErrInvalidOptions)
	_, err = parsePages(fstest.MapFS{
		"templates/layout.html":   {Data: []byte(`{{define "layout"}}{{end}}`)},
		"templates/partials.html": {Data: []byte(`{{define "p"}}{{end}}`)},
		"templates/bad.html":      {Data: []byte(`{{define "content"}}{{.Unclosed`)},
	})
	require.Error(t, err)
}

func TestNextHref(t *testing.T) {
	t.Parallel()
	assert.Empty(t, nextHref("/traces", nil, ""))
	vals, err := parseQuery("model=a&b=c&cursor=old&sort=cost")
	require.NoError(t, err)
	assert.Equal(t, "/traces?cursor=NEW&model=a&sort=cost", nextHref("/traces", vals, "NEW"))
	assert.Empty(t, sessionNext("/sessions", vals, ""))
	assert.Equal(t, "/sessions?cursor=N", sessionNext("/sessions", nil, "N"))
	assert.Empty(t, sessionNextDetail("/sessions/x", ""))
}

func parseQuery(q string) (url.Values, error) {
	v, err := url.ParseQuery(q)
	if err != nil {
		return nil, fmt.Errorf("parse query: %w", err)
	}
	return v, nil
}
