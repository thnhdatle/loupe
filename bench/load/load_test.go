// SPDX-License-Identifier: Apache-2.0

package load_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/thnhdatle/loupe/bench/gen"
	"github.com/thnhdatle/loupe/bench/load"
)

// fakeHTTP answers requests in rotation: 200, 429 with Retry-After, partial
// success rejecting 100 spans, 429 without Retry-After.
type fakeHTTP struct {
	n    atomic.Int64
	mu   sync.Mutex
	seqs map[uint32]int
	auth atomic.Value
}

func (f *fakeHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req coltrace.ExportTraceServiceRequest
	if r.URL.Path != "/v1/traces" || proto.Unmarshal(body, &req) != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	f.auth.Store(r.Header.Get("Authorization"))
	_, seq, _ := gen.PayloadSeq(&req)
	f.mu.Lock()
	f.seqs[seq]++
	f.mu.Unlock()
	switch f.n.Add(1) % 4 {
	case 1:
		w.WriteHeader(http.StatusOK)
	case 2:
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	case 3:
		b, _ := proto.Marshal(&coltrace.ExportTraceServiceResponse{
			PartialSuccess: &coltrace.ExportTracePartialSuccess{RejectedSpans: 100},
		})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(b)
	default:
		w.WriteHeader(http.StatusTooManyRequests)
	}
}

type fakeGRPC struct {
	coltrace.UnimplementedTraceServiceServer
	n atomic.Int64
}

func (f *fakeGRPC) Export(
	_ context.Context, _ *coltrace.ExportTraceServiceRequest,
) (*coltrace.ExportTraceServiceResponse, error) {
	if f.n.Add(1)%2 == 1 {
		st, _ := status.New(codes.ResourceExhausted, "full").WithDetails(&errdetails.RetryInfo{})
		return nil, st.Err()
	}
	return nil, status.Error(codes.ResourceExhausted, "full")
}

func startGRPC(t *testing.T) (string, *fakeGRPC) {
	t.Helper()
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &fakeGRPC{}
	s := grpc.NewServer()
	coltrace.RegisterTraceServiceServer(s, f)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return lis.Addr().String(), f
}

func TestRunHTTPClassification(t *testing.T) {
	t.Parallel()
	f := &fakeHTTP{seqs: map[uint32]int{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	run := func(mode string) load.Summary {
		sum, err := load.Run(t.Context(), load.Config{
			HTTPURL: srv.URL, Rate: 5120, Duration: time.Second, Mode: mode, Token: "tok",
		})
		require.NoError(t, err)
		return sum
	}
	s := run(load.ModeReject)
	assert.InDelta(t, 10, s.Requests, 2)
	assert.Equal(t, s.Requests, s.RequestsHTTP)
	assert.Zero(t, s.RequestsGRPC)
	assert.Zero(t, s.TransportErrors)
	assert.Zero(t, s.SkippedBacklog)
	assert.Equal(t, s.LaunchedSpans, s.SentSpans)
	assert.Equal(t, s.SentSpans, s.AcceptedSpans+s.RejectedSpans+s.DroppedSpans)
	assert.Zero(t, s.DroppedSpans)
	assert.Positive(t, s.AcceptedSpans)
	assert.Positive(t, s.HTTP429WithoutRetryAfter)
	assert.Equal(t, "Bearer tok", f.auth.Load())
	assert.Positive(t, s.LatencyP50ms)

	// Non-retrying: every sequence number arrived exactly once.
	for seq, n := range f.seqs {
		assert.Equal(t, 1, n, "seq %d", seq)
	}
	assert.Len(t, f.seqs, int(s.Requests))

	f.n.Store(0)
	d := run(load.ModeDrop)
	assert.Positive(t, d.DroppedSpans)
	assert.Zero(t, d.TransportErrors)
	assert.Equal(t, d.SentSpans, d.AcceptedSpans+d.RejectedSpans+d.DroppedSpans)
}

func TestRunGRPCFraction(t *testing.T) {
	t.Parallel()
	addr, g := startGRPC(t)
	f := &fakeHTTP{seqs: map[uint32]int{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	s, err := load.Run(t.Context(), load.Config{
		HTTPURL: srv.URL, GRPCAddr: addr, Rate: 10240, Duration: time.Second, Batch: 512,
		GRPCFraction: 0.1,
	})
	require.NoError(t, err)
	assert.InDelta(t, 20, s.Requests, 3)
	assert.InDelta(t, float64(s.Requests)/10, float64(s.RequestsGRPC), 1)
	assert.Equal(t, s.RequestsGRPC, g.n.Load())
	assert.Equal(t, s.RequestsGRPC, s.GRPCRejections)
	assert.InDelta(t, float64(s.RequestsGRPC)/2, float64(s.GRPCRejectionsWithoutRetryInfo), 1)
	assert.Zero(t, s.TransportErrors)
	assert.Equal(t, s.SentSpans, s.AcceptedSpans+s.RejectedSpans+s.DroppedSpans)
}

func TestRunTransportErrors(t *testing.T) {
	t.Parallel()
	// The server closes every connection without a response. A closed
	// server's port could be reused by a parallel test's server and answer.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()
	s, err := load.Run(t.Context(), load.Config{
		HTTPURL: srv.URL, Rate: 2560, Duration: time.Second, DrainTimeout: 10 * time.Second,
	})
	require.NoError(t, err)
	assert.Positive(t, s.Requests)
	assert.Zero(t, s.Unfinished)
	assert.Equal(t, s.Requests, s.TransportErrors)
	assert.Equal(t, s.LaunchedSpans, s.TransportErrorSpans)
	assert.Zero(t, s.SentSpans)
	// Every failed request is listed once by its sequence number.
	require.Len(t, s.TransportErrorSeqs, int(s.Requests))
	seen := map[uint32]bool{}
	for _, q := range s.TransportErrorSeqs {
		assert.False(t, seen[q], "seq %d listed twice", q)
		seen[q] = true
		assert.Less(t, int64(q), s.Requests)
	}
}

func TestRunOtherErrorsAndBacklog(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	time.AfterFunc(700*time.Millisecond, func() { close(release) })
	s, err := load.Run(t.Context(), load.Config{
		HTTPURL: srv.URL, Rate: 5120, Duration: 500 * time.Millisecond, MaxInflight: 2,
	})
	require.NoError(t, err)
	assert.Positive(t, s.SkippedBacklog)
	assert.EqualValues(t, 2, s.Requests)
	assert.EqualValues(t, 2, s.OtherErrors["http:500 Internal Server Error"])
	assert.EqualValues(t, 1024, s.OtherErrorSpans)
}

func TestRunConfigErrors(t *testing.T) {
	t.Parallel()
	for _, c := range []load.Config{
		{HTTPURL: "http://x", Duration: time.Second},
		{HTTPURL: "http://x", Rate: 1},
		{Rate: 1, Duration: time.Second},
		{HTTPURL: "http://x", Rate: 1, Duration: time.Second, Mode: "bogus"},
		{HTTPURL: "http://x", Rate: 1, Duration: time.Second, GRPCFraction: 0.5},
	} {
		_, err := load.Run(t.Context(), c)
		assert.Error(t, err)
	}
}
