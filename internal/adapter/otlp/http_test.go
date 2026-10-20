// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const (
	ctProto = "application/x-protobuf"
	ctJSON  = "application/json"
)

type httpReq struct {
	path     string
	ct       string
	encoding string
	body     []byte
	header   map[string]string
}

func do(t *testing.T, h http.Handler, r httpReq) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, r.path, bytes.NewReader(r.body))
	if r.ct != "" {
		req.Header.Set("Content-Type", r.ct)
	}
	if r.encoding != "" {
		req.Header.Set("Content-Encoding", r.encoding)
	}
	for k, v := range r.header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func protoReq(t *testing.T, path string, m proto.Message) httpReq {
	t.Helper()
	return httpReq{path: path, ct: ctProto, body: marshal(t, m)}
}

func statusOf(t *testing.T, rec *httptest.ResponseRecorder) *spb.Status {
	t.Helper()
	st := &spb.Status{}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), ctJSON) {
		require.NoError(t, protojson.Unmarshal(rec.Body.Bytes(), st))
	} else {
		require.NoError(t, proto.Unmarshal(rec.Body.Bytes(), st))
	}
	return st
}

func TestHTTP_Traces_Success(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  func(t *testing.T) httpReq
	}{
		{"protobuf", func(t *testing.T) httpReq {
			t.Helper()
			return protoReq(t, "/v1/traces", nSpans(2))
		}},
		{"protobuf with charset", func(t *testing.T) httpReq {
			t.Helper()
			r := protoReq(t, "/v1/traces", nSpans(2))
			r.ct = ctProto + "; charset=utf-8"
			return r
		}},
		{"protobuf gzip", func(t *testing.T) httpReq {
			t.Helper()
			r := protoReq(t, "/v1/traces", nSpans(2))
			r.body, r.encoding = gz(t, r.body), "gzip"
			return r
		}},
		{"protobuf identity encoding", func(t *testing.T) httpReq {
			t.Helper()
			r := protoReq(t, "/v1/traces", nSpans(2))
			r.encoding = "identity"
			return r
		}},
		{"json", func(t *testing.T) httpReq {
			t.Helper()
			b, err := protojson.Marshal(nSpans(2))
			require.NoError(t, err)
			return httpReq{path: "/v1/traces", ct: ctJSON, body: rewriteIDsToHex(t, b)}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			rec := do(t, f.srv.HTTPHandler(), tc.req(t))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Len(t, f.ing.Spans(), 2)
			assert.Zero(t, f.lim.InUse(), "in-flight bytes are released")
		})
	}
}

func TestHTTP_Traces_ProtobufAndJSONAreEquivalent(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/traces_genai.json")
	require.NoError(t, err)
	req := &coltrace.ExportTraceServiceRequest{}
	require.NoError(t, protojson.Unmarshal(hexToBase64(t, raw), req))

	fj := newFixture(t, nil)
	rec := do(t, fj.srv.HTTPHandler(), httpReq{path: "/v1/traces", ct: ctJSON, body: raw})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	fp := newFixture(t, nil)
	rec = do(t, fp.srv.HTTPHandler(), protoReq(t, "/v1/traces", req))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.Len(t, fj.ing.Spans(), 1)
	assert.Equal(t, fp.ing.Spans(), fj.ing.Spans())
	s := fj.ing.Spans()[0]
	assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", s.TraceID.String())
	assert.Equal(t, "1112131415161718", s.SpanID.String())
	assert.Equal(t, "2122232425262728", s.ParentSpanID.String())
	assert.EqualValues(t, 3500000000, s.EndNs)
	assert.EqualValues(t, 1, s.StatusCode)
	assert.Equal(t, "triage", s.Resource.Attributes[0].Value.Str)
	assert.Equal(t, int64(1200), s.Attributes[2].Value.Int)
	assert.Equal(t, int64(340), s.Attributes[3].Value.Int)
}

func TestHTTP_Traces_Errors(t *testing.T) {
	t.Parallel()
	good := marshal(t, nSpans(1))
	tests := []struct {
		name   string
		req    httpReq
		mutate func(*otlp.Config)
		want   int
	}{
		{"no content type", httpReq{path: "/v1/traces", body: good}, nil, http.StatusUnsupportedMediaType},
		{
			"bad content type",
			httpReq{path: "/v1/traces", ct: "text/plain", body: good},
			nil,
			http.StatusUnsupportedMediaType,
		},
		{
			"bad content encoding",
			httpReq{path: "/v1/traces", ct: ctProto, encoding: "br", body: good},
			nil,
			http.StatusUnsupportedMediaType,
		},
		{
			"bad gzip header",
			httpReq{path: "/v1/traces", ct: ctProto, encoding: "gzip", body: good},
			nil,
			http.StatusBadRequest,
		},
		{"truncated gzip", httpReq{
			path: "/v1/traces", ct: ctProto, encoding: "gzip",
			body: gz(t, good)[:15],
		}, nil, http.StatusBadRequest},
		{
			"bad protobuf",
			httpReq{path: "/v1/traces", ct: ctProto, body: []byte{0xff, 0xff, 0xff}},
			nil,
			http.StatusBadRequest,
		},
		{
			"bad json",
			httpReq{path: "/v1/traces", ct: ctJSON, body: []byte(`{"resourceSpans":`)},
			nil,
			http.StatusBadRequest,
		},
		{
			"bad json shape",
			httpReq{path: "/v1/traces", ct: ctJSON, body: []byte(`{"resourceSpans":5}`)},
			nil,
			http.StatusBadRequest,
		},
		{
			"non hex id",
			httpReq{
				path: "/v1/traces", ct: ctJSON,
				body: []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"zz"}]}]}]}`),
			},
			nil,
			http.StatusBadRequest,
		},
		{
			"too large identity",
			httpReq{path: "/v1/traces", ct: ctProto, body: good},
			func(c *otlp.Config) { c.MaxRequestBytes = int64(len(good)) - 1 }, http.StatusRequestEntityTooLarge,
		},
		{
			"too large gzip bomb",
			httpReq{
				path: "/v1/traces", ct: ctProto, encoding: "gzip",
				body: gz(t, bytes.Repeat([]byte{0}, 1<<20)),
			},
			func(c *otlp.Config) { c.MaxRequestBytes = 4096 }, http.StatusRequestEntityTooLarge,
		},
		{
			"too many spans", protoReq(t, "/v1/traces", nSpans(3)),
			func(c *otlp.Config) { c.MaxSpansPerRequest = 2 }, http.StatusBadRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, tc.mutate)
			rec := do(t, f.srv.HTTPHandler(), tc.req)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			assert.NotEmpty(t, statusOf(t, rec).GetMessage())
			assert.Empty(t, f.ing.Spans())
			assert.Zero(t, f.lim.InUse(), "in-flight bytes are released")
		})
	}
}

func TestHTTP_Traces_LimitRejectionsAreCounted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		req    httpReq
		mutate func(*otlp.Config)
		reason string
		n      int64
	}{
		{
			"too many spans", protoReq(t, "/v1/traces", nSpans(3)),
			func(c *otlp.Config) { c.MaxSpansPerRequest = 2 }, trace.ReasonTooManySpans, 3,
		},
		{
			"too large", protoReq(t, "/v1/traces", nSpans(3)),
			func(c *otlp.Config) { c.MaxRequestBytes = 10 }, trace.ReasonRequestTooBig, 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, tc.mutate)
			do(t, f.srv.HTTPHandler(), tc.req)
			assert.Equal(t, []trace.IngestCount{{
				Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: tc.reason, N: tc.n,
			}}, f.ing.Counts())
		})
	}
}

func TestHTTP_Traces_InflightLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	// Hold nearly the whole budget, as concurrent requests would.
	require.True(t, f.lim.TryAcquire(f.lim.InUse()+64<<20-5))
	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/traces", nSpans(2)))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))
	assert.Equal(t, int32(codes.ResourceExhausted), statusOf(t, rec).GetCode())
	assert.Empty(t, f.ing.Spans())
	assert.Equal(t, int64(64<<20-5), f.lim.InUse())
	assert.Equal(t, []trace.IngestCount{{
		Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonInflight, N: 1,
	}}, f.ing.Counts())
}

func TestHTTP_Traces_IngestErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		want       int
		retryAfter string
	}{
		{"overloaded", trace.ErrOverloaded, http.StatusTooManyRequests, "1"},
		{"shutting down", trace.ErrShuttingDown, http.StatusServiceUnavailable, ""},
		{"other", errors.New("boom"), http.StatusInternalServerError, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			f.ing.SetError(tc.err)
			rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/traces", nSpans(1)))
			assert.Equal(t, tc.want, rec.Code)
			assert.Equal(t, tc.retryAfter, rec.Header().Get("Retry-After"))
			assert.NotEmpty(t, statusOf(t, rec).GetMessage())
		})
	}
}

func TestHTTP_Traces_PartialSuccess(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.ing.SetResult(&trace.IngestResult{Accepted: 1, Dropped: 1, Message: "queue full, spans dropped"})
	req := nSpans(2)
	bad := testSpan("bad", 9)
	bad.TraceId = []byte{1, 2}
	req.ResourceSpans[0].ScopeSpans[0].Spans = append(req.ResourceSpans[0].ScopeSpans[0].Spans, bad)

	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/traces", req))
	require.Equal(t, http.StatusOK, rec.Code)
	resp := &coltrace.ExportTraceServiceResponse{}
	require.NoError(t, proto.Unmarshal(rec.Body.Bytes(), resp))
	assert.EqualValues(t, 2, resp.GetPartialSuccess().GetRejectedSpans())
	assert.Contains(t, resp.GetPartialSuccess().GetErrorMessage(), "queue full")
	assert.Contains(t, resp.GetPartialSuccess().GetErrorMessage(), "invalid trace or span id")
	assert.Equal(t, []trace.IngestCount{{
		Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonInvalid, N: 1,
	}}, f.ing.Counts())
}

func TestHTTP_Traces_InvalidSpansOnlyReportsPartialMessage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	bad := testSpan("bad", 1)
	bad.SpanId = nil
	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/traces", tracesRequest(bad)))
	require.Equal(t, http.StatusOK, rec.Code)
	resp := &coltrace.ExportTraceServiceResponse{}
	require.NoError(t, proto.Unmarshal(rec.Body.Bytes(), resp))
	assert.Equal(t, "1 spans with invalid trace or span id", resp.GetPartialSuccess().GetErrorMessage())
}

func TestHTTP_JSONResponseEncoding(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.ing.SetResult(&trace.IngestResult{Dropped: 1, Message: "dropped"})
	raw, err := os.ReadFile("testdata/traces_genai.json")
	require.NoError(t, err)
	rec := do(t, f.srv.HTTPHandler(), httpReq{path: "/v1/traces", ct: ctJSON, body: raw})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "json")
	resp := &coltrace.ExportTraceServiceResponse{}
	require.NoError(t, protojson.Unmarshal(rec.Body.Bytes(), resp))
	assert.EqualValues(t, 1, resp.GetPartialSuccess().GetRejectedSpans())

	f.ing.SetError(trace.ErrOverloaded)
	rec = do(t, f.srv.HTTPHandler(), httpReq{path: "/v1/traces", ct: ctJSON, body: raw})
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, int32(codes.ResourceExhausted), statusOf(t, rec).GetCode())
}

func TestHTTP_Auth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"none", nil, http.StatusUnauthorized},
		{"bearer ok", map[string]string{"Authorization": "Bearer s3cret"}, http.StatusOK},
		{"bearer lower scheme", map[string]string{"Authorization": "bearer other"}, http.StatusOK},
		{"x-loupe-token ok", map[string]string{"X-Loupe-Token": "other"}, http.StatusOK},
		{"bearer wrong", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"wrong scheme", map[string]string{"Authorization": "Basic s3cret"}, http.StatusUnauthorized},
		{"token without scheme", map[string]string{"Authorization": "s3cret"}, http.StatusUnauthorized},
		{"x-loupe-token wrong", map[string]string{"X-Loupe-Token": "nope"}, http.StatusUnauthorized},
		{"prefix of token", map[string]string{"Authorization": "Bearer s3"}, http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(c *otlp.Config) { c.Tokens = []string{"s3cret", "other"} })
			r := protoReq(t, "/v1/traces", nSpans(1))
			r.header = tc.header
			rec := do(t, f.srv.HTTPHandler(), r)
			assert.Equal(t, tc.want, rec.Code)
			if tc.want == http.StatusUnauthorized {
				assert.Equal(t, int32(codes.Unauthenticated), statusOf(t, rec).GetCode())
				assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
				assert.Empty(t, f.ing.Spans())
			}
		})
	}
}

func TestHTTP_Shutdown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.srv.BeginShutdown()
	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/traces", nSpans(1)))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Empty(t, f.ing.Spans())
}

func TestHTTP_Routing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/traces", http.NoBody)
	rec := httptest.NewRecorder()
	f.srv.HTTPHandler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = do(t, f.srv.HTTPHandler(), httpReq{path: "/v1/unknown", ct: ctProto})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHTTP_Logs(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	tid, sid := idBytes(16, 1), idBytes(8, 1)
	req := logsRequest(
		&logspb.LogRecord{
			TraceId: tid, SpanId: sid, EventName: "gen_ai.user.message",
			TimeUnixNano: 5, Body: strVal("hi"), Attributes: nil,
		},
		&logspb.LogRecord{ObservedTimeUnixNano: 7, Body: strVal("plain")},
		&logspb.LogRecord{TraceId: []byte{1}, SpanId: sid},
		&logspb.LogRecord{TraceId: tid, SpanId: []byte{1}},
	)
	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/logs", req))
	require.Equal(t, http.StatusOK, rec.Code)
	resp := &collogs.ExportLogsServiceResponse{}
	require.NoError(t, proto.Unmarshal(rec.Body.Bytes(), resp))
	assert.Nil(t, resp.GetPartialSuccess())

	logs := f.ing.Logs()
	require.Len(t, logs, 4)
	assert.Equal(t, "gen_ai.user.message", logs[0].EventName)
	assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", logs[0].TraceID.String())
	assert.Equal(t, "hi", logs[0].Body.Str)
	assert.EqualValues(t, 7, logs[1].TimeNs, "observed time stands in for a missing time")
	assert.True(t, logs[1].TraceID.IsZero())
	assert.True(t, logs[2].TraceID.IsZero(), "malformed ids mean no span context")
	assert.True(t, logs[3].SpanID.IsZero())
}

func TestHTTP_Logs_PartialSuccessAndErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.ing.SetResult(&trace.IngestResult{Rejected: 2, Message: "nope"})
	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/logs", logsRequest(&logspb.LogRecord{})))
	require.Equal(t, http.StatusOK, rec.Code)
	resp := &collogs.ExportLogsServiceResponse{}
	require.NoError(t, proto.Unmarshal(rec.Body.Bytes(), resp))
	assert.EqualValues(t, 2, resp.GetPartialSuccess().GetRejectedLogRecords())

	f.ing.SetError(trace.ErrOverloaded)
	rec = do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/logs", logsRequest(&logspb.LogRecord{})))
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestHTTP_Metrics_AreCountedAndDiscarded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	rec := do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/metrics", metricsRequest()))
	require.Equal(t, http.StatusOK, rec.Code)
	resp := &colmetrics.ExportMetricsServiceResponse{}
	require.NoError(t, proto.Unmarshal(rec.Body.Bytes(), resp))
	assert.Nil(t, resp.GetPartialSuccess())
	assert.Equal(t, []trace.IngestCount{{
		Signal: trace.SignalMetrics, Result: trace.ResultDiscarded, Reason: trace.ReasonNotStored, N: 6,
	}}, f.ing.Counts())
	assert.Empty(t, f.ing.Spans())

	// An empty metrics request is accepted and records nothing.
	f = newFixture(t, nil)
	rec = do(t, f.srv.HTTPHandler(), protoReq(t, "/v1/metrics", &colmetrics.ExportMetricsServiceRequest{}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, f.ing.Counts())
}

func TestHTTP_RecoversFromPanic(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	srv, err := otlp.NewServer(otlp.DefaultConfig(), panicIngestor{f.ing}, f.lim)
	require.NoError(t, err)
	rec := do(t, srv.HTTPHandler(), protoReq(t, "/v1/traces", nSpans(1)))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
