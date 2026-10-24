// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

var anchor = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func dbPath(tb testing.TB) string {
	tb.Helper()
	return filepath.Join(tb.TempDir(), "loupe.db")
}

func openAt(tb testing.TB, path string, opts ...sqlite.Option) *sqlite.Store {
	tb.Helper()
	opts = append([]sqlite.Option{sqlite.WithClock(clock.NewFake(anchor)), sqlite.WithReadPoolSize(4)}, opts...)
	s, err := sqlite.Open(context.Background(), path, opts...)
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = s.Close() })
	return s
}

func openStore(tb testing.TB, opts ...sqlite.Option) *sqlite.Store {
	tb.Helper()
	return openAt(tb, dbPath(tb), opts...)
}

func tid(n int) trace.ID {
	var id trace.ID
	id[0], id[14], id[15] = 0xab, byte(n>>8), byte(n)
	return id
}

func sid(n int) trace.SpanID {
	var id trace.SpanID
	if n == 0 {
		return id
	}
	id[0], id[6], id[7] = 0xcd, byte(n>>8), byte(n)
	return id
}

type spanOpt func(*trace.Span)

func mkSpan(tr, id, parent int, kind trace.Kind, opts ...spanOpt) trace.Span {
	start := anchor.Add(time.Duration(id) * time.Millisecond).UnixNano()
	sp := trace.Span{
		TraceID: tid(tr), SpanID: sid(id), ParentSpanID: sid(parent), ResourceID: 7,
		Service: "svc", Name: fmt.Sprintf("op-%d", id), Kind: kind, Status: trace.StatusOK,
		StartNs: start, EndNs: start + int64(100*time.Millisecond), Attributes: []byte(`{"k":"v"}`),
	}
	for _, o := range opts {
		o(&sp)
	}
	return sp
}

func usage(in, out int64) spanOpt {
	return func(s *trace.Span) { s.Usage.Input, s.Usage.Output, s.HasUsage = in, out, true }
}

func priced(n int64) spanOpt { return func(s *trace.Span) { s.Cost = trace.Priced(n) } }

func model(m string) spanOpt { return func(s *trace.Span) { s.Model, s.Provider = m, "openai" } }

func session(id string) spanOpt { return func(s *trace.Span) { s.SessionID = id } }

func user(id string) spanOpt { return func(s *trace.Span) { s.UserID = id } }

func failed() spanOpt {
	return func(s *trace.Span) { s.Status, s.ErrorType = trace.StatusError, "boom" }
}

func at(d time.Duration) spanOpt {
	return func(s *trace.Span) {
		dur := s.EndNs - s.StartNs
		s.StartNs = anchor.Add(d).UnixNano()
		s.EndNs = s.StartNs + dur
	}
}

func lasting(d time.Duration) spanOpt { return func(s *trace.Span) { s.EndNs = s.StartNs + int64(d) } }

func named(n string) spanOpt { return func(s *trace.Span) { s.Name = n } }

func service(n string) spanOpt { return func(s *trace.Span) { s.Service = n } }

func writeSpans(tb testing.TB, s *sqlite.Store, spans ...trace.Span) trace.BatchResult {
	tb.Helper()
	res, err := s.WriteBatch(context.Background(), &trace.Batch{
		Resources: []trace.Resource{{ID: 7, Attributes: []byte(`{"service.name":"svc"}`)}},
		Spans:     spans,
	})
	require.NoError(tb, err)
	return res
}

func rawDB(tb testing.TB, path string) *sql.DB {
	tb.Helper()
	db := openRaw(tb, path)
	tb.Cleanup(func() { _ = db.Close() })
	return db
}

func openRaw(tb testing.TB, path string) *sql.DB {
	tb.Helper()
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	require.NoError(tb, err)
	return db
}

func dump(tb testing.TB, db *sql.DB, q string) []string {
	tb.Helper()
	rows, err := db.QueryContext(context.Background(), q)
	require.NoError(tb, err)
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	require.NoError(tb, err)
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(tb, rows.Scan(ptrs...))
		parts := make([]string, len(vals))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				parts[i] = hex.EncodeToString(b)
			} else {
				parts[i] = fmt.Sprint(v)
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	require.NoError(tb, rows.Err())
	return out
}

func snapshot(tb testing.TB, path string) map[string][]string {
	tb.Helper()
	db := openRaw(tb, path)
	defer func() { _ = db.Close() }()
	return snapshotDB(tb, db)
}

func snapshotDB(tb testing.TB, db *sql.DB) map[string][]string {
	tb.Helper()
	return map[string][]string{
		"spans": dump(tb, db, `SELECT trace_id, span_id, parent_span_id, resource_id, service, name, kind, status,
			start_ns, end_ns, duration_ns, model, provider, input_tokens, output_tokens, cache_read_tokens,
			cache_write_tokens, reasoning_tokens, has_usage, cost_nanos, session_id, user_id, error_type, flags,
			content_stripped, rollup_user, eff_cache_read, eff_cache_write, eff_reasoning, eff_cost_nanos, attributes
			FROM spans ORDER BY trace_id, span_id`),
		"content": dump(tb, db, `SELECT trace_id, span_id, source, time_ns, key, part, role, bytes, body, truncated,
			has_span FROM span_content ORDER BY trace_id, span_id, source, time_ns, key, part`),
		"traces":           dump(tb, db, "SELECT * FROM traces ORDER BY trace_id"),
		"sessions":         dump(tb, db, "SELECT * FROM sessions ORDER BY id"),
		"rollup_hour":      dump(tb, db, "SELECT * FROM rollup_hour ORDER BY 1, 2, 3, 4, 5"),
		"rollup_day":       dump(tb, db, "SELECT * FROM rollup_day ORDER BY 1, 2, 3, 4, 5"),
		"rollup_user_hour": dump(tb, db, "SELECT * FROM rollup_user_hour ORDER BY 1, 2"),
		"rollup_user_day":  dump(tb, db, "SELECT * FROM rollup_user_day ORDER BY 1, 2"),
	}
}

func queryInt(tb testing.TB, db *sql.DB, q string, args ...any) int64 {
	tb.Helper()
	var n int64
	require.NoError(tb, db.QueryRowContext(context.Background(), q, args...).Scan(&n))
	return n
}
