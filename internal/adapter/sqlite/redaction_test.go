// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const (
	redEmail     = "alice.tester@example.com"
	redPhone     = "415-555-0132"
	redPhoneIntl = "+14155550132"
	redCard      = "4111111111111111"
	redCardSpace = "4242 4242 4242 4242"
	redKey       = "sk-proj-abcdefghijklmnopqrstuvwx1234" // gitleaks:allow
	redAWS       = "AKIAIOSFODNN7EXAMPLE"                 // gitleaks:allow
)

var redSecrets = []string{
	redEmail, redPhone, redPhoneIntl, redCard, redCardSpace, redKey, redAWS,
	"4155550132", "4242424242424242",
}

var redMarkers = []string{"[EMAIL]", "[PHONE]", "[CREDIT_CARD]", "[API_KEY]"}

const redText = "mail " + redEmail + " phone " + redPhone + " or " + redPhoneIntl +
	" cards " + redCard + " and " + redCardSpace + " keys " + redKey + " and " + redAWS

type redPipeline struct {
	store   *sqlite.Store
	path    string
	handler http.Handler
	stop    func()
}

func newRedPipeline(t *testing.T, policy content.Policy) *redPipeline {
	t.Helper()
	path := dbPath(t)
	clk := clock.System{}
	store := openAt(t, path, sqlite.WithClock(clk), sqlite.WithContentMaxBytes(policy.MaxBytes))
	w, err := writer.New(store, clk, writer.WithBatchSpans(1000), writer.WithBatchInterval(10*time.Millisecond))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	table, err := cost.NewPriceTable(nil, nil)
	require.NoError(t, err)
	calc, err := cost.NewCalculator(table)
	require.NoError(t, err)
	ing, err := ingest.New(w, calc, clk, ingest.WithSyncAck(true), ingest.WithContentPolicy(policy))
	require.NoError(t, err)
	lim, err := ingest.NewInflightLimiter(ingest.DefaultMaxInflightBytes)
	require.NoError(t, err)
	srv, err := otlp.NewServer(otlp.DefaultConfig(), ing, lim)
	require.NoError(t, err)
	p := &redPipeline{store: store, path: path, handler: srv.HTTPHandler()}
	p.stop = func() {
		cancel()
		require.NoError(t, <-done)
		_, err := store.Checkpoint(context.Background())
		require.NoError(t, err)
		require.NoError(t, store.Close())
	}
	return p
}

func (p *redPipeline) post(t *testing.T, route string, body any) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, route, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

type jm = map[string]any

func sattr(k, v string) jm { return jm{"key": k, "value": jm{"stringValue": v}} }

const (
	redTrace = "0102030405060708090a0b0c0d0e0f10"
	redSpan  = "1112131415161718"
)

func redTraces(span jm) jm {
	return jm{"resourceSpans": []jm{{
		"resource":   jm{"attributes": []jm{sattr("service.name", "red-svc")}},
		"scopeSpans": []jm{{"scope": jm{"name": "t"}, "spans": []jm{span}}},
	}}}
}

func redSpanDoc(attrs, events []jm) jm {
	return jm{
		"traceId": redTrace, "spanId": redSpan, "name": "chat red-model", "kind": 3,
		"startTimeUnixNano": "1000000000", "endTimeUnixNano": "2000000000",
		"attributes": attrs, "events": events, "status": jm{"code": 1},
	}
}

func redLogs(recs ...jm) jm {
	return jm{"resourceLogs": []jm{{
		"resource":  jm{"attributes": []jm{sattr("service.name", "red-svc")}},
		"scopeLogs": []jm{{"scope": jm{"name": "t"}, "logRecords": recs}},
	}}}
}

func redLog(ns, event, body string, attrs ...jm) jm {
	return jm{
		"timeUnixNano": ns, "traceId": redTrace, "spanId": redSpan, "body": jm{"stringValue": body},
		"attributes": append([]jm{sattr("event.name", event)}, attrs...),
	}
}

func redInputMessages(text string) string {
	b, _ := json.Marshal([]jm{{"role": "user", "parts": []jm{{"type": "text", "content": text}}}})
	return string(b)
}

func redRawScan(t *testing.T, path string, needles []string) map[string]int {
	t.Helper()
	found := map[string]int{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		for _, n := range needles {
			if c := bytes.Count(data, []byte(n)); c > 0 {
				found[suffix+" "+n] += c
			}
		}
	}
	return found
}

type redRow struct {
	source, key, role string
	bytes             int
	body              []byte
	hasBody           bool
	truncated         bool
}

func redContent(t *testing.T, path string) []redRow {
	t.Helper()
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(64<<20))
	require.NoError(t, err)
	defer dec.Close()
	rows, err := rawDB(t, path).QueryContext(context.Background(),
		"SELECT source, key, role, bytes, body, truncated FROM span_content ORDER BY id")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []redRow
	for rows.Next() {
		var r redRow
		var body []byte
		var trunc int
		require.NoError(t, rows.Scan(&r.source, &r.key, &r.role, &r.bytes, &body, &trunc))
		r.truncated = trunc != 0
		if body != nil {
			r.hasBody = true
			if bytes.HasPrefix(body, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
				body, err = dec.DecodeAll(body, nil)
				require.NoError(t, err)
			}
			r.body = body
		}
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func redQueryString(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	var s string
	require.NoError(t, db.QueryRowContext(context.Background(), q).Scan(&s))
	return s
}

func TestRedactionCaptureOnLeavesNoSecret(t *testing.T) {
	t.Parallel()
	red, err := content.NewRedactor([]string{
		content.PresetEmail, content.PresetPhone, content.PresetCreditCard, content.PresetAPIKey,
	}, nil)
	require.NoError(t, err)
	p := newRedPipeline(t, content.Policy{Mode: content.ModeOn, MaxBytes: 1 << 20, Redactor: red})

	p.post(t, "/v1/traces", redTraces(redSpanDoc([]jm{
		sattr("gen_ai.operation.name", "chat"),
		sattr("gen_ai.request.model", "red-model"),
		sattr("gen_ai.input.messages", redInputMessages(redText)),
		sattr("gen_ai.output.messages", redInputMessages(redText)),
		sattr("input.value", redText),
		sattr("output.value", redText),
		sattr("llm.input_messages.0.message.content", redText),
		sattr("llm.output_messages.0.message.content", redText),
		sattr("app.note", "support contact "+redEmail+" card "+redCard),
		sattr("app.key", redKey),
	}, []jm{
		{"timeUnixNano": "1100000000", "name": "gen_ai.user.message", "attributes": []jm{sattr("content", redText)}},
		{"timeUnixNano": "1200000000", "name": "gen_ai.choice", "attributes": []jm{sattr("message", redText)}},
	})))
	p.post(t, "/v1/logs", redLogs(
		redLog("1300000000", "gen_ai.user.message", redText),
		redLog("1400000000", "gen_ai.assistant.message", redText, sattr("extra", redText)),
	))
	p.stop()

	assert.Empty(t, redRawScan(t, p.path, redSecrets), "secret in raw database bytes")

	rows := redContent(t, p.path)
	require.NotEmpty(t, rows)
	sources := map[string]bool{}
	var joined strings.Builder
	for _, r := range rows {
		sources[r.source] = true
		for _, s := range redSecrets {
			assert.NotContains(t, string(r.body), s, "secret in content %s/%s", r.source, r.key)
		}
		joined.Write(r.body)
		joined.WriteByte('\n')
	}
	for _, m := range redMarkers {
		assert.Contains(t, joined.String(), m, "marker missing from decompressed content")
	}
	assert.Greater(t, len(sources), 1, "content from several sources expected: %v", sources)
	keys := map[string]bool{}
	for _, r := range rows {
		keys[r.key] = true
	}
	for _, k := range []string{
		"gen_ai.input.messages", "gen_ai.output.messages", "input.value", "output.value",
		"llm.input_messages.0.message.content", "gen_ai.user.message",
	} {
		assert.True(t, keys[k], "content key %s not stored; have %v", k, keys)
	}

	db := rawDB(t, p.path)
	attrs := redQueryString(t, db, "SELECT attributes FROM spans")
	assert.Contains(t, attrs, "[EMAIL]")
	assert.Contains(t, attrs, "[CREDIT_CARD]")
	assert.Contains(t, attrs, "[API_KEY]")
	for _, s := range redSecrets {
		assert.NotContains(t, attrs, s)
	}
	res := redQueryString(t, db, "SELECT group_concat(CAST(attributes AS TEXT)) FROM resources")
	assert.NotContains(t, res, "[EMAIL]")
}

func TestRedactionMetadataOnlyStoresNoText(t *testing.T) {
	t.Parallel()
	red, err := content.NewRedactor([]string{content.PresetEmail}, nil)
	require.NoError(t, err)
	p := newRedPipeline(t, content.Policy{Mode: content.ModeMetadataOnly, MaxBytes: 1 << 20, Redactor: red})
	const marker = "ZZ-CONTENT-MARKER-7731"
	text := "prompt text " + marker + " " + redEmail
	p.post(t, "/v1/traces", redTraces(redSpanDoc([]jm{
		sattr("gen_ai.operation.name", "chat"),
		sattr("gen_ai.input.messages", redInputMessages(text)),
		sattr("input.value", text),
	}, []jm{
		{"timeUnixNano": "1100000000", "name": "gen_ai.user.message", "attributes": []jm{sattr("content", text)}},
	})))
	p.post(t, "/v1/logs", redLogs(redLog("1300000000", "gen_ai.user.message", text)))
	p.stop()

	assert.Empty(t, redRawScan(t, p.path, []string{marker, redEmail, "prompt text"}))
	rows := redContent(t, p.path)
	require.NotEmpty(t, rows)
	for _, r := range rows {
		assert.False(t, r.hasBody, "body must be NULL for %s/%s", r.source, r.key)
		assert.Positive(t, r.bytes, "bytes recorded for %s/%s", r.source, r.key)
	}
	assert.Equal(t, 1, redCountRole(rows, "user", "log"), "role kept on the log marker: %+v", rows)
	assert.Equal(t, "1", redQueryString(t, rawDB(t, p.path), "SELECT content_stripped FROM spans"))
}

func redCountRole(rows []redRow, role, source string) int {
	n := 0
	for _, r := range rows {
		if r.role == role && r.source == source {
			n++
		}
	}
	return n
}

func TestRedactionMaxBytesTruncates(t *testing.T) {
	t.Parallel()
	const limit = 64
	p := newRedPipeline(t, content.Policy{Mode: content.ModeOn, MaxBytes: limit})
	long := strings.Repeat("abcdefghij", 50)
	p.post(t, "/v1/traces", redTraces(redSpanDoc([]jm{
		sattr("gen_ai.operation.name", "chat"),
		sattr("input.value", long),
	}, nil)))
	p.stop()

	rows := redContent(t, p.path)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].truncated)
	assert.Equal(t, len(long), rows[0].bytes, "bytes keeps the original size")
	assert.LessOrEqual(t, len(rows[0].body), limit)
}

func redLogNs(i int) string { return "1000000" + strconv.Itoa(100+i) }

const redLimit = 100

func redSpanBudget(t *testing.T, path string, wantTruncated bool) []redRow {
	t.Helper()
	rows := redContent(t, path)
	total, truncated := 0, false
	for _, r := range rows {
		require.True(t, r.hasBody, "capture on stores a body (possibly empty)")
		total += len(r.body)
		truncated = truncated || r.truncated
	}
	assert.LessOrEqual(t, total, redLimit, "stored body bytes of one span exceed content.max_bytes")
	assert.Equal(t, wantTruncated, truncated, "truncation marker")
	if wantTruncated {
		assert.Equal(t, redLimit, total, "the budget is used up to the redLimit (ASCII bodies)")
	}
	return rows
}

func TestRedactionMaxBytesAcrossLogEvents(t *testing.T) {
	t.Parallel()
	const limit, records = redLimit, 5
	p := newRedPipeline(t, content.Policy{Mode: content.ModeOn, MaxBytes: limit})
	recs := make([]jm, 0, records)
	for i := range records {
		recs = append(recs, redLog(redLogNs(i), "gen_ai.user.message", strings.Repeat("x", 80)))
	}
	p.post(t, "/v1/logs", redLogs(recs...))
	p.stop()

	rows := redSpanBudget(t, p.path, true)
	require.Len(t, rows, records)
	for i, r := range rows {
		assert.Equal(t, 80, r.bytes)
		assert.Equal(t, i > 0, r.truncated, "row %d", i)
	}
	assert.Len(t, rows[0].body, 80)
	assert.Len(t, rows[1].body, 20)
}

func TestRedactionMaxBytesLogsBeforeSpan(t *testing.T) {
	t.Parallel()
	const limit = redLimit
	p := newRedPipeline(t, content.Policy{Mode: content.ModeOn, MaxBytes: limit})
	p.post(t, "/v1/logs", redLogs(redLog(redLogNs(0), "gen_ai.user.message", strings.Repeat("a", 40))))
	p.post(t, "/v1/logs", redLogs(redLog(redLogNs(1), "gen_ai.user.message", strings.Repeat("b", 40))))
	p.post(t, "/v1/traces", redTraces(redSpanDoc([]jm{
		sattr("gen_ai.operation.name", "chat"),
		sattr("input.value", strings.Repeat("c", 80)),
	}, nil)))
	p.stop()

	rows := redSpanBudget(t, p.path, true)
	require.Len(t, rows, 3)
	assert.Equal(t, "cccccccccccccccccccc", string(rows[2].body), "the span part gets the rest")
	assert.True(t, rows[2].truncated)
	assert.Equal(t, 80, rows[2].bytes)
}

func TestRedactionMaxBytesSpanThenLogs(t *testing.T) {
	t.Parallel()
	const limit = redLimit
	p := newRedPipeline(t, content.Policy{Mode: content.ModeOn, MaxBytes: limit})
	p.post(t, "/v1/traces", redTraces(redSpanDoc([]jm{
		sattr("gen_ai.operation.name", "chat"),
		sattr("input.value", strings.Repeat("c", 70)),
	}, nil)))
	p.post(t, "/v1/logs", redLogs(
		redLog(redLogNs(0), "gen_ai.user.message", strings.Repeat("a", 50)),
		redLog(redLogNs(1), "gen_ai.user.message", strings.Repeat("b", 5)),
	))
	p.post(t, "/v1/logs", redLogs(redLog(redLogNs(2), "gen_ai.user.message", "z")))
	p.stop()

	rows := redSpanBudget(t, p.path, true)
	require.Len(t, rows, 4)
	assert.Len(t, rows[0].body, 70)
	assert.False(t, rows[0].truncated)
	assert.Len(t, rows[1].body, 30)
	for _, r := range rows[1:] {
		assert.True(t, r.truncated)
	}
	assert.Empty(t, rows[2].body)
	assert.Empty(t, rows[3].body)
}

func TestRedactionMaxBytesResentLogsNotCounted(t *testing.T) {
	t.Parallel()
	const limit = redLimit
	p := newRedPipeline(t, content.Policy{Mode: content.ModeOn, MaxBytes: limit})
	first := redLog(redLogNs(0), "gen_ai.user.message", strings.Repeat("a", 60))
	p.post(t, "/v1/logs", redLogs(first))
	p.post(t, "/v1/logs", redLogs(first, first))
	p.post(t, "/v1/logs", redLogs(first, redLog(redLogNs(1), "gen_ai.user.message", strings.Repeat("b", 40))))
	p.stop()

	rows := redSpanBudget(t, p.path, false)
	require.Len(t, rows, 2)
	assert.Len(t, rows[0].body, 60)
	assert.Len(t, rows[1].body, 40)
}

func TestRedactionMaxBytesCountsRedactedText(t *testing.T) {
	t.Parallel()
	const limit = redLimit
	red, err := content.NewRedactor(nil, []content.Rule{{Name: "x", Pattern: "x", Replacement: "yy"}})
	require.NoError(t, err)
	p := newRedPipeline(t, content.Policy{Mode: content.ModeOn, MaxBytes: limit, Redactor: red})
	p.post(t, "/v1/logs", redLogs(redLog(redLogNs(0), "gen_ai.user.message", strings.Repeat("x", 30))))
	p.post(t, "/v1/logs", redLogs(redLog(redLogNs(1), "gen_ai.user.message", strings.Repeat("x", 30))))
	p.stop()

	rows := redSpanBudget(t, p.path, true)
	require.Len(t, rows, 2)
	assert.Equal(t, strings.Repeat("y", 60), string(rows[0].body))
	assert.Equal(t, strings.Repeat("y", 40), string(rows[1].body))
	assert.Equal(t, 30, rows[1].bytes, "bytes is the original size")
}
