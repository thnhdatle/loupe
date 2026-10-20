//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite" // database/sql driver "sqlite".

	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/acceptance/internal/native"
	"github.com/thnhdatle/loupe/internal/domain/semconv"
)

// lpe10Prefix starts every marker; a marker is the prefix and 8 hex digits.
const lpe10Prefix = "LPEMK"

// lpe10Batch is the number of spans per OTLP request.
const lpe10Batch = 250

// lpe10EventNames are the per-message GenAI event names (span events and logs).
var lpe10EventNames = []string{
	"gen_ai.system.message", "gen_ai.user.message", "gen_ai.assistant.message",
	"gen_ai.tool.message", "gen_ai.choice", semconv.OperationDetailsEvent,
}

// lpe10Gen hands out unique markers and counts them.
type lpe10Gen struct{ n int }

func (g *lpe10Gen) marker() string {
	g.n++
	return fmt.Sprintf("%s%08x", lpe10Prefix, g.n)
}

// lpe10Keys returns concrete attribute keys for every rule of the denylist:
// the rule sample plus the pattern with each `*` replaced by "1" and by "x.7".
func lpe10Keys() []string {
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, r := range semconv.ContentKeyRules() {
		add(r.Sample)
		add(r.Pattern)
		if strings.Contains(r.Pattern, "*") {
			add(strings.ReplaceAll(r.Pattern, "*", "1"))
			add(strings.ReplaceAll(r.Pattern, "*", "x.7"))
		}
	}
	slices.Sort(keys)
	return keys
}

func lpe10Str(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func lpe10Int(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

func lpe10StrVal(v string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}
}

// lpe10Value is the carrier value of a key: a JSON message list for the
// gen_ai.*.messages style keys, a JSON object for structured keys, else text.
func (g *lpe10Gen) value(key string) string {
	switch {
	case strings.HasSuffix(key, ".messages") || strings.HasSuffix(key, "system_instructions"):
		b, _ := json.Marshal([]map[string]any{{
			"role": "user", "parts": []map[string]string{{"type": "text", "content": g.marker()}},
		}})
		return string(b)
	case strings.HasSuffix(key, "arguments") || strings.HasSuffix(key, "parameters"):
		b, _ := json.Marshal(map[string]string{"q": g.marker()})
		return string(b)
	default:
		return g.marker()
	}
}

// lpe10Span builds span number i with markers in every content carrier.
func (g *lpe10Gen) span(i int, keys []string, now time.Time) *tracepb.Span {
	tid := make([]byte, 16)
	sid := make([]byte, 8)
	tid[0], tid[1] = 0x11, 0x22
	for b := range 4 {
		tid[15-b] = byte(i >> (8 * b))
		sid[7-b] = byte(i >> (8 * b))
	}
	sid[0] = 0x33
	start := now.Add(-time.Duration(i%600) * time.Second)
	sp := &tracepb.Span{
		TraceId: tid, SpanId: sid, Name: "chat gpt-4o", Kind: tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(start.UnixNano()),
		EndTimeUnixNano:   uint64(start.Add(120 * time.Millisecond).UnixNano()),
		Attributes: []*commonpb.KeyValue{
			lpe10Str("gen_ai.operation.name", "chat"),
			lpe10Str("gen_ai.system", "openai"),
			lpe10Str("gen_ai.request.model", "gpt-4o"),
			lpe10Int("gen_ai.usage.input_tokens", 100),
			lpe10Int("gen_ai.usage.output_tokens", 20),
		},
	}
	for _, k := range keys {
		sp.Attributes = append(sp.Attributes, lpe10Str(k, g.value(k)))
	}
	for j, name := range lpe10EventNames {
		ev := &tracepb.Span_Event{
			Name: name, TimeUnixNano: uint64(start.UnixNano()) + uint64(j+1)*1000,
			Attributes: []*commonpb.KeyValue{
				lpe10Str("content", g.marker()),
				lpe10Str("role", "user"),
				lpe10Str("extra", g.marker()),
				lpe10Str("gen_ai.system", "openai"),
			},
		}
		if name == semconv.OperationDetailsEvent {
			ev.Attributes = append(ev.Attributes, lpe10Str("gen_ai.input.messages", g.value("gen_ai.input.messages")))
		}
		sp.Events = append(sp.Events, ev)
	}
	return sp
}

// lpe10Logs builds GenAI log events for the span, one per event-name source,
// with markers in body and attributes, plus one non-GenAI event.
func (g *lpe10Gen) logs(sp *tracepb.Span, i int, now time.Time) []*logspb.LogRecord {
	mk := func(name string, source int) *logspb.LogRecord {
		rec := &logspb.LogRecord{
			TimeUnixNano: uint64(now.UnixNano()) + uint64(i*10+source),
			TraceId:      sp.TraceId, SpanId: sp.SpanId,
			Attributes: []*commonpb.KeyValue{lpe10Str("extra", g.marker()), lpe10Str("gen_ai.system", "openai")},
		}
		switch source {
		case 0:
			rec.EventName = name
		case 1:
			rec.Attributes = append(rec.Attributes, lpe10Str("event.name", name))
		default:
			rec.Attributes = append(rec.Attributes, lpe10Str("otel.event.name", name))
		}
		if name == semconv.OperationDetailsEvent {
			kvs := make([]*commonpb.KeyValue, 0, 3)
			for _, k := range []string{"gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.system_instructions"} {
				kvs = append(kvs, lpe10Str(k, g.value(k)))
			}
			rec.Body = lpe10KV(kvs)
		} else {
			rec.Body = lpe10KV([]*commonpb.KeyValue{lpe10Str("content", g.marker()), lpe10Str("role", "assistant")})
			if (i+source)%3 == 0 { // string body variant
				rec.Body = lpe10StrVal(g.marker())
			}
		}
		return rec
	}
	var out []*logspb.LogRecord
	for s := range 3 {
		out = append(out, mk(lpe10EventNames[(i+s)%len(lpe10EventNames)], s))
	}
	out = append(out, mk(semconv.OperationDetailsEvent, i%3))
	other := mk("app.custom.event", 0)
	other.Body = lpe10StrVal(g.marker())
	return append(out, other)
}

func lpe10Post(ctx context.Context, url string, msg proto.Message) error {
	body, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("POST %s: %s: %s", url, resp.Status, b)
	}
	return nil
}

// lpe10Send posts spans and log events in batches and returns the markers generated.
func lpe10Send(ctx context.Context, p *native.Proc, spans int, keys []string) (int, error) {
	g := &lpe10Gen{}
	now := time.Now()
	res := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{lpe10Str("service.name", "lpe10")}}
	for lo := 0; lo < spans; lo += lpe10Batch {
		var sps []*tracepb.Span
		var recs []*logspb.LogRecord
		for i := lo; i < min(lo+lpe10Batch, spans); i++ {
			sp := g.span(i, keys, now)
			sps = append(sps, sp)
			recs = append(recs, g.logs(sp, i, now)...)
		}
		tr := &coltrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: res, ScopeSpans: []*tracepb.ScopeSpans{{Spans: sps}},
		}}}
		if err := lpe10Post(ctx, p.HTTPURL()+"/v1/traces", tr); err != nil {
			return g.n, err
		}
		lr := &collogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
			Resource: res, ScopeLogs: []*logspb.ScopeLogs{{LogRecords: recs}},
		}}}
		if err := lpe10Post(ctx, p.HTTPURL()+"/v1/logs", lr); err != nil {
			return g.n, err
		}
	}
	return g.n, nil
}

type lpe10Stats struct {
	Stored          int64 `json:"stored"`
	ContentStripped int64 `json:"content_stripped"`
	QueueSpans      int64 `json:"queue_spans"`
}

func lpe10GetStats(ctx context.Context, p *native.Proc) (lpe10Stats, error) {
	var st lpe10Stats
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UIURL()+"/v1/ingest/stats", nil)
	if err != nil {
		return st, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return st, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return st, fmt.Errorf("ingest/stats: %s", resp.Status)
	}
	return st, json.NewDecoder(resp.Body).Decode(&st)
}

// lpe10Drain waits until the queue is empty and want spans are stored.
func lpe10Drain(ctx context.Context, p *native.Proc, want int64) (lpe10Stats, error) {
	for {
		st, err := lpe10GetStats(ctx, p)
		if err == nil && st.Stored >= want && st.QueueSpans == 0 {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return st, fmt.Errorf("drain: stored %d of %d: %w (last error: %w)", st.Stored, want, ctx.Err(), err)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// lpe10Scanned totals what the scans looked at and found.
type lpe10Scanned struct {
	found, rawBytes, blobs, blobBytes, logBytes int
	where                                       []string
}

func (s *lpe10Scanned) note(where string, n int) {
	if n > 0 {
		s.found += n
		s.where = append(s.where, fmt.Sprintf("%s=%d", where, n))
	}
}

// scanRaw counts markers in the database, WAL and SHM files.
func (s *lpe10Scanned) scanRaw(tb testing.TB, phase, dbPath string) {
	tb.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(dbPath + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			tb.Fatalf("read %s: %v", dbPath+suffix, err)
		}
		s.rawBytes += len(data)
		s.note(phase+" raw "+filepath.Base(dbPath)+suffix, bytes.Count(data, []byte(lpe10Prefix)))
	}
}

// scanSQL reads every column of every table through SQL; blobs holding a zstd
// frame are decompressed with the store's codec settings and scanned too.
func (s *lpe10Scanned) scanSQL(tb testing.TB, dbPath string) {
	tb.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		tb.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(64<<20))
	if err != nil {
		tb.Fatalf("zstd: %v", err)
	}
	defer dec.Close()
	for _, table := range lpe10Tables(ctx, tb, db) {
		s.scanTable(ctx, tb, db, dec, table)
	}
}

func lpe10Tables(ctx context.Context, tb testing.TB, db *sql.DB) []string {
	tb.Helper()
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		tb.Fatalf("list tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			tb.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, n)
	}
	if err := rows.Err(); err != nil {
		tb.Fatalf("list tables: %v", err)
	}
	return tables
}

func (s *lpe10Scanned) scanTable(ctx context.Context, tb testing.TB, db *sql.DB, dec *zstd.Decoder, table string) {
	tb.Helper()
	rows, err := db.QueryContext(ctx, "SELECT * FROM \""+table+"\"")
	if err != nil {
		tb.Fatalf("select %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		tb.Fatalf("columns %s: %v", table, err)
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			tb.Fatalf("scan %s: %v", table, err)
		}
		for i, v := range vals {
			s.scanValue(tb, dec, table+"."+cols[i], v)
		}
	}
	if err := rows.Err(); err != nil {
		tb.Fatalf("rows %s: %v", table, err)
	}
}

// scanValue scans a text or blob value and, for a zstd frame, its decompressed body.
func (s *lpe10Scanned) scanValue(tb testing.TB, dec *zstd.Decoder, where string, v any) {
	tb.Helper()
	var b []byte
	switch x := v.(type) {
	case string:
		b = []byte(x)
	case []byte:
		b = x
	default:
		return
	}
	s.note("sql "+where, bytes.Count(b, []byte(lpe10Prefix)))
	if !bytes.HasPrefix(b, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		return
	}
	out, err := dec.DecodeAll(b, nil)
	if err != nil {
		tb.Fatalf("decompress %s: %v", where, err)
	}
	s.blobs++
	s.blobBytes += len(out)
	s.note("blob "+where, bytes.Count(out, []byte(lpe10Prefix)))
}

// lpe10Count returns one integer from the database, read-only.
func lpe10Count(tb testing.TB, dbPath, query string) int {
	tb.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		tb.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		tb.Fatalf("%s: %v", query, err)
	}
	return n
}

// TestAcceptance_LPE10_NoContentLeak is the acceptance test of criterion LPE-10:
// the native binary runs with content.capture off and debug logging, receives
// spans and GenAI log events whose every content carrier holds a unique marker,
// and neither the database files, any decompressed blob nor the log contains one.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE10_NoContentLeak(t *testing.T) {
	run := harness.Start(t, "LPE-10")
	spans := int(lpe09Threshold(t, run, "spans"))
	wantFound := int(lpe09Threshold(t, run, "markers_found"))

	p := native.Start(t, native.Opts{Env: map[string]string{
		"LOUPE_CONTENT_CAPTURE": "off", "LOUPE_LOG_LEVEL": "debug",
	}})
	keys := lpe10Keys()
	measured := map[string]any{"spans": spans, "carriers": len(keys)}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	markers, err := lpe10Send(ctx, p, spans, keys)
	measured["markers_sent"] = markers
	if err != nil {
		run.Fail(measured, "send: %v", err)
	}
	st, drainErr := lpe10Drain(ctx, p, int64(spans))
	if drainErr != nil {
		run.Fail(measured, "%v", drainErr)
	}
	measured["stored"] = st.Stored
	measured["content_stripped"] = st.ContentStripped

	var sc lpe10Scanned
	sc.scanRaw(t, "running", p.DBPath)
	if stopErr := p.Stop(); stopErr != nil {
		run.Fail(measured, "stop: %v", stopErr)
	}
	sc.scanRaw(t, "stopped", p.DBPath)
	sc.scanSQL(t, p.DBPath)
	logData, err := os.ReadFile(p.LogPath)
	if err != nil {
		run.Fail(measured, "read log: %v", err)
	}
	sc.logBytes = len(logData)
	sc.note("log", bytes.Count(logData, []byte(lpe10Prefix)))

	stored := lpe10Count(t, p.DBPath, "SELECT count(*) FROM spans")
	contentRows := lpe10Count(t, p.DBPath, "SELECT count(*) FROM span_content")
	measured["spans_in_db"] = stored
	measured["span_content_rows"] = contentRows
	measured["markers_found"] = sc.found
	measured["raw_bytes_scanned"] = sc.rawBytes
	measured["blobs_scanned"] = sc.blobs
	measured["blob_bytes_scanned"] = sc.blobBytes
	measured["log_bytes_scanned"] = sc.logBytes

	summary := fmt.Sprintf("%d spans, %d markers sent in %d carrier keys; found %d (%s); "+
		"%d raw bytes, %d blobs, %d log bytes scanned; content_stripped %d, content rows %d",
		stored, markers, len(keys), sc.found, strings.Join(sc.where, ", "),
		sc.rawBytes, sc.blobs, sc.logBytes, st.ContentStripped, contentRows)
	switch {
	case stored != spans || st.Stored != int64(spans):
		run.Fail(measured, "vacuous: stored spans %d (api %d), want %d; %s", stored, st.Stored, spans, summary)
	case st.ContentStripped == 0:
		run.Fail(measured, "vacuous: content_stripped is 0; %s", summary)
	case contentRows == 0:
		run.Fail(measured, "vacuous: no content marker rows; %s", summary)
	case sc.logBytes == 0 || sc.rawBytes == 0:
		run.Fail(measured, "vacuous: nothing scanned; %s", summary)
	case sc.found > wantFound:
		run.Fail(measured, "content leaked: %s", summary)
	}
	run.Pass(measured, summary)
}

func lpe10KV(kvs []*commonpb.KeyValue) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: kvs}}}
}
