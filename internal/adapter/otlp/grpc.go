// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	_ "google.golang.org/grpc/encoding/gzip" // registers the gzip compressor
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// admitReserve is the most a gRPC call reserves at admission, before its real
// decompressed size is known.
const admitReserve = 1 << 20

const (
	traceServicePrefix   = "/opentelemetry.proto.collector.trace.v1.TraceService/"
	logsServicePrefix    = "/opentelemetry.proto.collector.logs.v1.LogsService/"
	metricsServicePrefix = "/opentelemetry.proto.collector.metrics.v1.MetricsService/"
)

func signalOf(fullMethod string) trace.Signal {
	switch {
	case strings.HasPrefix(fullMethod, traceServicePrefix):
		return trace.SignalTraces
	case strings.HasPrefix(fullMethod, logsServicePrefix):
		return trace.SignalLogs
	case strings.HasPrefix(fullMethod, metricsServicePrefix):
		return trace.SignalMetrics
	default:
		return ""
	}
}

// callState follows one gRPC call from admission (tap) to completion (stats End),
// because grpc-go gives no single hook that spans both and a call can fail between
// them, for example while decompressing an oversized message.
type callState struct {
	signal  trace.Signal
	held    atomic.Int64 // in-flight bytes reserved for this call
	entered atomic.Bool  // the handler ran, so errors after that point are ours
}

type callStateKey struct{}

func stateFrom(ctx context.Context) *callState {
	st, _ := ctx.Value(callStateKey{}).(*callState)
	return st
}

// ensureState returns the call's state, creating it if neither tap nor stats ran yet;
// grpc-go does not document their order.
func ensureState(ctx context.Context) (context.Context, *callState) {
	if st := stateFrom(ctx); st != nil {
		return ctx, st
	}
	st := &callState{}
	return context.WithValue(ctx, callStateKey{}, st), st
}

// releaseHeld returns the call's reservation to the limiter, once.
func (s *Server) releaseHeld(st *callState) {
	if st == nil {
		return
	}
	s.lim.Release(st.held.Swap(0))
}

// tap admits a call: shutdown, auth, then a reservation of
// min(max_request_bytes, 1 MiB) in the in-flight limiter.
func (s *Server) tap(ctx context.Context, info *tap.Info) (context.Context, error) {
	sig := signalOf(info.FullMethodName)
	if sig == "" {
		return ctx, nil
	}
	if s.shutting.Load() {
		return ctx, statusErr(codes.Unavailable, "receiver is shutting down")
	}
	if !s.auth.check(firstMD(info.Header, "authorization"), firstMD(info.Header, "x-loupe-token")) {
		return ctx, statusErr(codes.Unauthenticated, "missing or invalid token")
	}
	ctx, st := ensureState(ctx)
	st.signal = sig
	reserve := min(s.cfg.MaxRequestBytes, admitReserve)
	if !s.lim.TryAcquire(reserve) {
		s.count(sig, trace.ResultRejected, trace.ReasonInflight, 1)
		return ctx, overloadedStatus("in-flight byte limit reached, retry later")
	}
	st.held.Store(reserve)
	return ctx, nil
}

func firstMD(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

// callStats releases reservations when a call ends and counts messages grpc-go
// rejected for size before the handler saw them.
type callStats struct{ s *Server }

func (c callStats) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	ctx, st := ensureState(ctx)
	if st.signal == "" {
		st.signal = signalOf(info.FullMethodName)
	}
	return ctx
}

func (c callStats) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	end, ok := rs.(*stats.End)
	if !ok {
		return
	}
	st := stateFrom(ctx)
	if st == nil {
		return
	}
	if end.Error != nil && !st.entered.Load() && st.signal != "" &&
		status.Code(end.Error) == codes.ResourceExhausted {
		c.s.count(st.signal, trace.ResultRejected, trace.ReasonRequestTooBig, 1)
	}
	c.s.releaseHeld(st)
}

func (callStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }

func (callStats) HandleConn(context.Context, stats.ConnStats) {}

// adjust replaces the admission reservation with the real decompressed size.
func (s *Server) adjust(st *callState, size int64) error {
	diff := size - st.held.Load()
	switch {
	case diff > 0:
		if !s.lim.TryAcquire(diff) {
			return trace.ErrOverloaded
		}
		st.held.Add(diff)
	case diff < 0:
		s.lim.Release(-diff)
		st.held.Add(diff)
	}
	return nil
}

// begin marks the handler as entered and settles the in-flight accounting.
func (s *Server) begin(ctx context.Context, req proto.Message) (*callState, error) {
	st := stateFrom(ctx)
	if st == nil {
		// Not admitted through tap (for example a test calling the service directly).
		st = &callState{}
	}
	st.entered.Store(true)
	if err := s.adjust(st, int64(proto.Size(req))); err != nil {
		if st.signal != "" {
			// The request is decoded, so the rejection is counted in its
			// items (spans, log records, data points), the unit of every
			// other ingest counter of the signal.
			s.count(st.signal, trace.ResultRejected, trace.ReasonInflight, max(1, itemCount(req)))
		}
		s.releaseHeld(st)
		return nil, s.grpcError(err)
	}
	return st, nil
}

// itemCount is the number of spans, log records or metric data points of an
// OTLP export request.
func itemCount(req proto.Message) int64 {
	switch r := req.(type) {
	case *coltrace.ExportTraceServiceRequest:
		return int64(countSpans(r.GetResourceSpans()))
	case *collogs.ExportLogsServiceRequest:
		return int64(countLogRecords(r.GetResourceLogs()))
	case *colmetrics.ExportMetricsServiceRequest:
		return countDataPoints(r.GetResourceMetrics())
	default:
		return 0
	}
}

// statusErr returns a gRPC status error; it is the call's return value, so it is
// deliberately not wrapped.
func statusErr(c codes.Code, msg string) error {
	return status.Error(c, msg) //nolint:wrapcheck // a status error is the gRPC reply itself
}

func statusError(st *status.Status) error {
	return st.Err() //nolint:wrapcheck // a status error is the gRPC reply itself
}

func overloadedStatus(msg string) error {
	st := status.New(codes.ResourceExhausted, msg)
	withInfo, err := st.WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(time.Second)})
	if err != nil {
		return statusError(st)
	}
	return statusError(withInfo)
}

// grpcError maps an ingest error to its gRPC status.
func (s *Server) grpcError(err error) error {
	switch {
	case errors.Is(err, errTooManySpans):
		return statusErr(codes.InvalidArgument, err.Error())
	case isOverloaded(err):
		return overloadedStatus("ingest queue is full, retry later")
	case errors.Is(err, trace.ErrShuttingDown):
		return statusErr(codes.Unavailable, "receiver is shutting down")
	case errors.Is(err, context.Canceled):
		return statusErr(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return statusErr(codes.DeadlineExceeded, "deadline exceeded")
	default:
		s.log.Error("ingest failed", "error", err)
		return statusErr(codes.Internal, "internal error")
	}
}

type traceService struct {
	coltrace.UnimplementedTraceServiceServer
	s *Server
}

func (t traceService) Export(ctx context.Context, req *coltrace.ExportTraceServiceRequest) (
	*coltrace.ExportTraceServiceResponse, error,
) {
	st, err := t.s.begin(ctx, req)
	if err != nil {
		return nil, err
	}
	defer t.s.releaseHeld(st)
	resp, err := t.s.exportTraces(ctx, req)
	if err != nil {
		return nil, t.s.grpcError(err)
	}
	return resp, nil
}

type logsService struct {
	collogs.UnimplementedLogsServiceServer
	s *Server
}

func (l logsService) Export(ctx context.Context, req *collogs.ExportLogsServiceRequest) (
	*collogs.ExportLogsServiceResponse, error,
) {
	st, err := l.s.begin(ctx, req)
	if err != nil {
		return nil, err
	}
	defer l.s.releaseHeld(st)
	resp, err := l.s.exportLogs(ctx, req)
	if err != nil {
		return nil, l.s.grpcError(err)
	}
	return resp, nil
}

type metricsService struct {
	colmetrics.UnimplementedMetricsServiceServer
	s *Server
}

func (m metricsService) Export(ctx context.Context, req *colmetrics.ExportMetricsServiceRequest) (
	*colmetrics.ExportMetricsServiceResponse, error,
) {
	st, err := m.s.begin(ctx, req)
	if err != nil {
		return nil, err
	}
	defer m.s.releaseHeld(st)
	return m.s.exportMetrics(req), nil
}

// recoverInterceptor turns a handler panic into INTERNAL.
func (s *Server) recoverInterceptor(
	ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
) (resp any, err error) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("panic in otlp grpc handler", "method", info.FullMethod,
				"error", fmt.Sprint(p), "stack", string(debug.Stack()))
			err = statusErr(codes.Internal, "internal error")
		}
	}()
	return handler(ctx, req)
}

// GRPCServer returns a grpc.Server with the Trace, Logs and Metrics services
// registered. Extra options are applied after the receiver's own.
func (s *Server) GRPCServer(extra ...grpc.ServerOption) *grpc.Server {
	opts := []grpc.ServerOption{
		grpc.InTapHandle(s.tap),
		grpc.StatsHandler(callStats{s: s}),
		grpc.MaxRecvMsgSize(int(min(s.cfg.MaxRequestBytes, math.MaxInt32))),
		grpc.ChainUnaryInterceptor(s.recoverInterceptor),
	}
	if s.cfg.GRPCTLS != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(s.cfg.GRPCTLS)))
	}
	srv := grpc.NewServer(append(opts, extra...)...)
	coltrace.RegisterTraceServiceServer(srv, traceService{s: s})
	collogs.RegisterLogsServiceServer(srv, logsService{s: s})
	colmetrics.RegisterMetricsServiceServer(srv, metricsService{s: s})
	return srv
}

// RunGRPC serves OTLP/gRPC on ln until ctx ends, then stops accepting and drains
// in-flight calls for at most Config.ShutdownTimeout before cutting them off.
func (s *Server) RunGRPC(ctx context.Context, ln net.Listener) error {
	srv := s.GRPCServer()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
		case <-done:
			return
		}
		s.BeginShutdown()
		timer := time.AfterFunc(s.cfg.ShutdownTimeout, srv.Stop)
		defer timer.Stop()
		srv.GracefulStop()
	}()
	err := srv.Serve(ln)
	close(done)
	<-stopped
	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("otlp grpc serve: %w", err)
	}
	return nil
}
