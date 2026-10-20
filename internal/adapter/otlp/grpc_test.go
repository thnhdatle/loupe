// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// dial serves f on an in-memory listener and returns a client connection.
func dial(t *testing.T, f *fixture) *grpc.ClientConn {
	t.Helper()
	ln := bufconn.Listen(1 << 20)
	srv := f.srv.GRPCServer()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, conn.Close())
		srv.Stop()
		<-done
	})
	return conn
}

func retryDelay(t *testing.T, err error) time.Duration {
	t.Helper()
	for _, d := range status.Convert(err).Details() {
		if ri, ok := d.(*errdetails.RetryInfo); ok {
			return ri.GetRetryDelay().AsDuration()
		}
	}
	return 0
}

func TestGRPC_Traces_Success(t *testing.T) {
	t.Parallel()
	for _, comp := range []string{"", "gzip"} {
		t.Run("compressor="+comp, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			client := coltrace.NewTraceServiceClient(dial(t, f))
			var opts []grpc.CallOption
			if comp != "" {
				opts = append(opts, grpc.UseCompressor(comp))
			}
			resp, err := client.Export(t.Context(), nSpans(3), opts...)
			require.NoError(t, err)
			assert.Nil(t, resp.GetPartialSuccess())
			assert.Len(t, f.ing.Spans(), 3)
			assert.Zero(t, f.lim.InUse(), "in-flight bytes are released")
		})
	}
}

func TestGRPC_Traces_PartialSuccess(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.ing.SetResult(&trace.IngestResult{Dropped: 2, Message: "queue full, spans dropped"})
	resp, err := coltrace.NewTraceServiceClient(dial(t, f)).Export(t.Context(), nSpans(2))
	require.NoError(t, err)
	assert.EqualValues(t, 2, resp.GetPartialSuccess().GetRejectedSpans())
	assert.Equal(t, "queue full, spans dropped", resp.GetPartialSuccess().GetErrorMessage())
}

func TestGRPC_Traces_IngestErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		err   error
		want  codes.Code
		retry time.Duration
	}{
		{"overloaded carries RetryInfo", trace.ErrOverloaded, codes.ResourceExhausted, time.Second},
		{"shutting down", trace.ErrShuttingDown, codes.Unavailable, 0},
		{"canceled", context.Canceled, codes.Canceled, 0},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded, 0},
		{"other", assert.AnError, codes.Internal, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			f.ing.SetError(tc.err)
			_, err := coltrace.NewTraceServiceClient(dial(t, f)).Export(t.Context(), nSpans(1))
			require.Error(t, err)
			assert.Equal(t, tc.want, status.Code(err))
			assert.Equal(t, tc.retry, retryDelay(t, err))
			assert.Zero(t, f.lim.InUse())
		})
	}
}

func TestGRPC_Traces_TooManySpans(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *otlp.Config) { c.MaxSpansPerRequest = 2 })
	_, err := coltrace.NewTraceServiceClient(dial(t, f)).Export(t.Context(), nSpans(3))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Equal(t, []trace.IngestCount{{
		Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonTooManySpans, N: 3,
	}}, f.ing.Counts())
}

func TestGRPC_InflightAdmission(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	// Leave less room than the 1 MiB admission reservation.
	require.True(t, f.lim.TryAcquire(64<<20-1000))
	_, err := coltrace.NewTraceServiceClient(dial(t, f)).Export(t.Context(), nSpans(1))
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.Equal(t, time.Second, retryDelay(t, err))
	assert.Equal(t, int64(64<<20-1000), f.lim.InUse(), "a rejected call holds nothing")
	assert.Empty(t, f.ing.Spans())
	assert.Equal(t, []trace.IngestCount{{
		Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonInflight, N: 1,
	}}, f.ing.Counts())
}

func TestGRPC_InflightActualSizeExceedsLimit(t *testing.T) {
	t.Parallel()
	// Admission reserves 1 MiB; the request is 1.5 MiB and only 1000 spare bytes remain.
	f := newFixture(t, func(c *otlp.Config) { c.MaxRequestBytes = 4 << 20 })
	span := testSpan("big", 1)
	span.Attributes = append(span.Attributes, strKV("blob", strings.Repeat("x", 3<<19)))
	const held = 64<<20 - 1<<20 - 1000
	require.True(t, f.lim.TryAcquire(held))
	_, err := coltrace.NewTraceServiceClient(dial(t, f)).Export(t.Context(),
		tracesRequest(span, testSpan("small", 2), testSpan("small", 3)))
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.Equal(t, time.Second, retryDelay(t, err))
	assert.EqualValues(t, held, f.lim.InUse(), "the reservation is returned on failure")
	assert.Empty(t, f.ing.Spans())
	// The request was decoded, so the rejection is counted in spans.
	assert.Equal(t, []trace.IngestCount{{
		Signal: trace.SignalTraces, Result: trace.ResultRejected, Reason: trace.ReasonInflight, N: 3,
	}}, f.ing.Counts())
}

func TestGRPC_InflightSmallRequestReleasesReservation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	_, err := coltrace.NewTraceServiceClient(dial(t, f)).Export(t.Context(), nSpans(1))
	require.NoError(t, err)
	assert.Zero(t, f.lim.InUse())
}

func TestGRPC_RequestTooLargeAfterDecompression(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *otlp.Config) { c.MaxRequestBytes = 2 << 10 })
	client := coltrace.NewTraceServiceClient(dial(t, f))
	_, err := client.Export(t.Context(), nSpans(100), grpc.UseCompressor("gzip"))
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.Empty(t, f.ing.Spans())
	assert.Zero(t, f.lim.InUse(), "admission reservation is released when grpc rejects the message")
	counts := f.ing.Counts()
	require.Len(t, counts, 1)
	assert.Equal(t, trace.ReasonRequestTooBig, counts[0].Reason)
	assert.Equal(t, trace.SignalTraces, counts[0].Signal)
}

func TestGRPC_Auth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		md   metadata.MD
		want codes.Code
	}{
		{"none", nil, codes.Unauthenticated},
		{"bearer ok", metadata.Pairs("authorization", "Bearer s3cret"), codes.OK},
		{"x-loupe-token ok", metadata.Pairs("x-loupe-token", "s3cret"), codes.OK},
		{"wrong", metadata.Pairs("authorization", "Bearer nope"), codes.Unauthenticated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(c *otlp.Config) { c.Tokens = []string{"s3cret"} })
			ctx := t.Context()
			if tc.md != nil {
				ctx = metadata.NewOutgoingContext(ctx, tc.md)
			}
			_, err := coltrace.NewTraceServiceClient(dial(t, f)).Export(ctx, nSpans(1))
			assert.Equal(t, tc.want, status.Code(err))
			assert.Zero(t, f.lim.InUse())
		})
	}
}

func TestGRPC_Shutdown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.srv.BeginShutdown()
	_, err := coltrace.NewTraceServiceClient(dial(t, f)).Export(t.Context(), nSpans(1))
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestGRPC_Logs(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	conn := dial(t, f)
	client := collogs.NewLogsServiceClient(conn)
	rec := &logspb.LogRecord{TraceId: idBytes(16, 1), SpanId: idBytes(8, 1), EventName: "gen_ai.choice"}
	resp, err := client.Export(t.Context(), logsRequest(rec))
	require.NoError(t, err)
	assert.Nil(t, resp.GetPartialSuccess())
	require.Len(t, f.ing.Logs(), 1)
	assert.Equal(t, "gen_ai.choice", f.ing.Logs()[0].EventName)

	f.ing.SetResult(&trace.IngestResult{Rejected: 1, Message: "x"})
	resp, err = client.Export(t.Context(), logsRequest(rec))
	require.NoError(t, err)
	assert.EqualValues(t, 1, resp.GetPartialSuccess().GetRejectedLogRecords())

	f.ing.SetError(trace.ErrOverloaded)
	_, err = client.Export(t.Context(), logsRequest(rec))
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.Equal(t, time.Second, retryDelay(t, err))
}

func TestGRPC_Metrics_AreCountedAndDiscarded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	resp, err := colmetrics.NewMetricsServiceClient(dial(t, f)).Export(t.Context(), metricsRequest())
	require.NoError(t, err)
	assert.Nil(t, resp.GetPartialSuccess())
	assert.Equal(t, []trace.IngestCount{{
		Signal: trace.SignalMetrics, Result: trace.ResultDiscarded, Reason: trace.ReasonNotStored, N: 6,
	}}, f.ing.Counts())
}

func TestGRPC_RecoversFromPanic(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	srv, err := otlp.NewServer(otlp.DefaultConfig(), panicIngestor{f.ing}, f.lim)
	require.NoError(t, err)
	f.srv = srv
	_, err = coltrace.NewTraceServiceClient(dial(t, f)).Export(t.Context(), nSpans(1))
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Zero(t, f.lim.InUse())
}
