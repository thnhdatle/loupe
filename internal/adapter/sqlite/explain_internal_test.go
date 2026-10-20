// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

type planCase struct {
	name string
	sql  string
	args []any
	// want are substrings every one of which must appear in the plan; none
	// of avoid may appear (e.g. a full scan of spans).
	want  []string
	avoid []string
}

// plan returns EXPLAIN QUERY PLAN output indented like the sqlite3 shell.
func plan(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	rows, err := s.r.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	depth := map[int64]int{0: 0}
	var b strings.Builder
	for rows.Next() {
		var id, parent, unused int64
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		depth[id] = depth[parent] + 1
		fmt.Fprintf(&b, "%s%s\n", strings.Repeat("  ", depth[id]-1), detail)
	}
	require.NoError(t, rows.Err())
	return b.String()
}

func planCases(t *testing.T) []planCase {
	t.Helper()
	anchor := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	day, week, month := anchor.Add(-24*time.Hour), anchor.Add(-7*24*time.Hour), anchor.Add(-30*24*time.Hour)
	tid := make([]byte, 16)
	must := func(q string, args []any, _ int, err error) (string, []any) {
		require.NoError(t, err)
		return q, args
	}
	listPlan := func(f query.TraceFilter, srt query.Sort, cursor string, walk bool) (string, []any) {
		return must(listTracesSQL(&f, srt, query.OrderDesc, query.Page{Limit: 50, Cursor: cursor}, walk))
	}
	list := func(f query.TraceFilter, srt query.Sort, cursor string) (string, []any) {
		return listPlan(f, srt, cursor, false)
	}
	q1, a1 := list(query.TraceFilter{From: day, To: anchor}, query.SortStart, "")
	q2, a2 := list(query.TraceFilter{From: week, To: anchor, Span: query.SpanFilter{Model: "m", Status: "error"}},
		query.SortStart, "")
	q3, a3 := list(query.TraceFilter{From: week, To: anchor, Span: query.SpanFilter{
		Service:       "svc",
		MinDurationNs: int64(5 * time.Second),
	}}, query.SortStart, "")
	q4, a4 := listPlan(query.TraceFilter{From: month, To: anchor}, query.SortCost, "", true)
	q4n, a4n := list(query.TraceFilter{From: day, To: anchor}, query.SortCost, "")
	curCost := query.EncodeCursor(query.Cursor{SortKey: 1, ID: trace.ID{}.String()}, query.SortCost, query.OrderDesc)
	q4p, a4p := listPlan(query.TraceFilter{From: month, To: anchor}, query.SortCost, curCost, true)
	qDurW, aDurW := listPlan(query.TraceFilter{From: month, To: anchor}, query.SortDuration, "", true)
	q8, a8 := list(query.TraceFilter{From: week, To: anchor, Span: query.SpanFilter{NamePrefix: "agent."}},
		query.SortStart, "")
	cur := query.EncodeCursor(query.Cursor{SortKey: 1, ID: trace.ID{}.String()}, query.SortStart, query.OrderDesc)
	qPage, aPage := list(query.TraceFilter{From: day, To: anchor}, query.SortStart, cur)
	qUser, aUser := list(query.TraceFilter{From: day, To: anchor, UserID: "u"}, query.SortStart, "")
	qDur, aDur := list(query.TraceFilter{From: week, To: anchor}, query.SortDuration, "")
	q5, a5 := must(sessionTracesSQL("sess", query.Page{Limit: 200}))
	qSess, aSess := must(listSessionsSQL(query.SessionFilter{From: day, To: anchor}, query.Page{}))
	qSessU, aSessU := must(listSessionsSQL(query.SessionFilter{From: day, To: anchor, UserID: "u"}, query.Page{}))
	r := rollup.Range{FromNs: month.UnixNano(), ToNs: anchor.UnixNano()}
	q7d, a7d := rollupsSQL(tableRollupDay, r, rollup.Filter{})
	q7h, a7h := rollupsSQL(tableRollupHour, rollup.Range{
		FromNs: anchor.Add(-time.Hour).UnixNano(),
		ToNs:   anchor.UnixNano(),
	}, rollup.Filter{Kind: trace.KindLLM})
	qRe, aRe := recomputeSQL(admin.Range{Since: month, Until: anchor}, 0, 5000)
	ws := writeSQL()
	selHist, _, _ := rollupTables()[rtHour].sql()
	spanIdx := "sqlite_autoindex_spans_1 (trace_id=?"
	return []planCase{
		{name: "q1: traces, 24 h, first page", sql: q1, args: a1, want: []string{"traces_start"}},
		{
			name: "q2: traces, 7 d, model and status error", sql: q2, args: a2,
			want: []string{"traces_start", spanIdx}, avoid: []string{"SCAN s"},
		},
		{
			name: "q3: traces, 7 d, service and min duration", sql: q3, args: a3,
			want: []string{"traces_start", spanIdx}, avoid: []string{"SCAN s"},
		},
		// A range covering most stored traces walks traces_cost in order
		// (sortIndexPlan); a narrow one reads the range and sorts it.
		{
			name: "q4: traces, 30 d, by cost", sql: q4, args: a4,
			want: []string{"traces_cost"}, avoid: []string{"TEMP B-TREE"},
		},
		{
			name: "traces, by cost, wide range, next page", sql: q4p, args: a4p,
			want: []string{"traces_cost ((cost_nanos,trace_id)<(?,?))"}, avoid: []string{"TEMP B-TREE"},
		},
		{
			name: "traces, by duration, wide range", sql: qDurW, args: aDurW,
			want: []string{"traces_duration"}, avoid: []string{"TEMP B-TREE"},
		},
		{name: "traces, 24 h, by cost", sql: q4n, args: a4n, want: []string{"traces_start"}},
		{
			name: "traces, start bounds (sort plan choice)", sql: traceBoundsSQL,
			want: []string{"SEARCH traces USING COVERING INDEX traces_start"}, avoid: []string{"SCAN traces"},
		},
		{name: "q5: session detail traces", sql: q5, args: a5, want: []string{"traces_session"}},
		{name: "q5: session row", sql: getSessionSQL, args: []any{"sess"}, want: []string{"PRIMARY KEY"}},
		{name: "q6: trace row", sql: getTraceSQL, args: []any{tid}, want: []string{"PRIMARY KEY"}},
		{name: "q6: trace spans", sql: spanViewSQL(traceSpansTail), args: []any{tid}, want: []string{spanIdx}},
		{
			name: "q6: trace content", sql: contentSQL(true), args: []any{tid},
			want: []string{"sqlite_autoindex_span_content_1"},
		},
		{name: "q7: day rows", sql: q7d, args: a7d, want: []string{"PRIMARY KEY (bucket_ns>? AND bucket_ns<?)"}},
		{name: "q7: edge hour rows", sql: q7h, args: a7h, want: []string{"PRIMARY KEY (bucket_ns>? AND bucket_ns<?)"}},
		{name: "q8: traces, 7 d, name prefix", sql: q8, args: a8, want: []string{"traces_start", spanIdx}},
		{name: "traces, next page (keyset)", sql: qPage, args: aPage, want: []string{"traces_start"}},
		{name: "traces, by user", sql: qUser, args: aUser, want: []string{"traces_"}},
		{name: "traces, by duration", sql: qDur, args: aDur, want: []string{"traces_start"}},
		{name: "sessions, by last activity", sql: qSess, args: aSess, want: []string{"sessions_last"}},
		{name: "sessions, by user", sql: qSessU, args: aSessU, want: []string{"sessions_user"}},
		{
			name: "top spans by cost", sql: spanViewSQL(topSpansTail(query.TopByCost)),
			args: []any{"llm", 0, 1, 50}, want: []string{"spans_start"},
		},
		{
			name: "top spans by duration", sql: spanViewSQL(topSpansTail(query.TopByDuration)),
			args: []any{"llm", 0, 1, 50}, want: []string{"spans_start"},
		},
		{name: "ingest totals", sql: ingestTotalsSQL},
		{
			name: "user rollups", sql: userRollupsSQL(tableRollupUserDay), args: []any{0, 1},
			want: []string{"PRIMARY KEY (bucket_ns>? AND bucket_ns<?)"},
		},
		{name: "write: trace row", sql: ws[stSelTrace], args: []any{tid}, want: []string{"PRIMARY KEY"}},
		{name: "write: dedupe load", sql: ws[stSelTraceSpans], args: []any{tid}, want: []string{spanIdx}},
		{
			name: "write: attach content", sql: ws[stMarkContent], args: []any{tid, tid[:8]},
			want: []string{"sqlite_autoindex_span_content_1"},
		},
		{
			name: "write: insert content (span probe)", sql: ws[stInsContent],
			args: []any{tid, tid[:8], "log", 0, "k", 0, "", 0, nil, 0, 0}, want: []string{spanIdx},
		},
		{
			name: "write: content budget", sql: ws[stSelContentBudget], args: []any{tid, tid[:8]},
			want: []string{"sqlite_autoindex_span_content_1 (trace_id=? AND span_id=?)"},
		},
		{name: "write: session row", sql: ws[stSelSession], args: []any{"s"}, want: []string{"PRIMARY KEY"}},
		{
			name: "write: rollup histogram", sql: selHist, args: []any{0, "", "", "", ""},
			want: []string{"PRIMARY KEY"},
		},
		{name: "retention: content", sql: oldContentSQL, args: []any{0, 1}, want: []string{"span_content_time"}},
		{
			name: "retention: orphan content", sql: orphanContentSQL, args: []any{0, 1},
			want: []string{"span_content_orphan", spanIdx},
		},
		{name: "retention: spans", sql: oldSpansSQL, args: []any{0, 1}, want: []string{"spans_start"}},
		{
			name: "retention: span content", sql: deleteSpanContentSQL, args: []any{tid, tid[:8]},
			want: []string{"sqlite_autoindex_span_content_1"},
		},
		{name: "retention: empty trace", sql: deleteEmptyTraceSQL, args: []any{tid}, want: []string{spanIdx}},
		{
			name: "retention: sessions", sql: emptySessionsSQL, args: []any{0, 1},
			want: []string{"sessions_last", "traces_session"},
		},
		{
			name: "retention: rollup_hour", sql: bucketTable{tableRollupHour, bucketCol, rollupKeyCols, 0}.selectSQL(),
			args: []any{0, 1}, want: []string{"PRIMARY KEY (bucket_ns<?)"},
		},
		{
			name: "retention: ingest_stats",
			sql:  bucketTable{"ingest_stats", "hour_ns", "hour_ns, signal, result, reason", 0}.selectSQL(),
			args: []any{0, 1}, want: []string{"PRIMARY KEY (hour_ns<?)"},
		},
		{name: "admin: recompute chunk", sql: qRe, args: aRe, want: []string{"INTEGER PRIMARY KEY (rowid>?)"}},
		{name: "admin: user trace", sql: userTraceSQL, args: []any{"u"}, want: []string{"traces_user", "spans_user"}},
		{
			name: "admin: user day rollups", sql: "SELECT bucket_ns, user_id FROM rollup_user_day WHERE user_id = ?1 LIMIT ?2",
			args: []any{"u", 1}, want: []string{"rollup_user_day_user"},
		},
	}
}

// TestSchema_QueryPlans checks that every query of the store uses the index
// listed for it in planCases. Run with -v to print the plans.
func TestSchema_QueryPlans(t *testing.T) {
	t.Parallel()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "plan.db"),
		WithClock(clock.NewFake(time.Unix(0, 0))))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	for _, tc := range planCases(t) {
		p := plan(t, s, tc.sql, tc.args...)
		t.Logf("### %s\n%s", tc.name, p)
		for _, w := range tc.want {
			assert.Contains(t, p, w, tc.name)
		}
		for _, a := range tc.avoid {
			assert.NotContains(t, p, a, tc.name)
		}
	}
}
