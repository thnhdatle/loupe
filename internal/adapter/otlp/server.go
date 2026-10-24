// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var errTooManySpans = trace.ErrTooManySpans

// Server holds what the HTTP and gRPC receivers share.
type Server struct {
	cfg      Config
	ing      trace.Ingestor
	lim      *ingest.InflightLimiter
	auth     *authenticator
	log      *slog.Logger
	shutting atomic.Bool
}

// NewServer returns a Server that feeds ing.
func NewServer(cfg Config, ing trace.Ingestor, lim *ingest.InflightLimiter) (*Server, error) {
	if ing == nil || lim == nil {
		return nil, fmt.Errorf("%w: ingestor and in-flight limiter are required", ErrInvalidConfig)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	lg := cfg.Logger
	if lg == nil {
		lg = slog.New(slog.DiscardHandler)
	}
	return &Server{
		cfg:  cfg,
		ing:  ing,
		lim:  lim,
		auth: newAuthenticator(cfg.Tokens),
		log:  lg.With("component", "otlp"),
	}, nil
}

func (s *Server) BeginShutdown() { s.shutting.Store(true) }

func (s *Server) count(sig trace.Signal, res trace.Result, reason string, n int64) {
	if n > 0 {
		s.ing.Count(trace.IngestCount{Signal: sig, Result: res, Reason: reason, N: n})
	}
}

func (s *Server) exportTraces(ctx context.Context, req *coltrace.ExportTraceServiceRequest) (
	*coltrace.ExportTraceServiceResponse, error,
) {
	rss := req.GetResourceSpans()
	if n := countSpans(rss); n > s.cfg.MaxSpansPerRequest {
		s.count(trace.SignalTraces, trace.ResultRejected, trace.ReasonTooManySpans, int64(n))
		return nil, fmt.Errorf("%w: %d spans, limit %d", errTooManySpans, n, s.cfg.MaxSpansPerRequest)
	}
	spans, invalid := convertSpans(rss)
	s.count(trace.SignalTraces, trace.ResultRejected, trace.ReasonInvalid, int64(invalid))
	res, err := s.ing.IngestSpans(ctx, spans)
	if err != nil {
		return nil, fmt.Errorf("ingest spans: %w", err)
	}
	resp := &coltrace.ExportTraceServiceResponse{}
	rejected := int64(res.Rejected + res.Dropped + invalid)
	if rejected > 0 {
		resp.PartialSuccess = &coltrace.ExportTracePartialSuccess{
			RejectedSpans: rejected,
			ErrorMessage:  partialMessage(res.Message, invalid),
		}
	}
	return resp, nil
}

func (s *Server) exportLogs(ctx context.Context, req *collogs.ExportLogsServiceRequest) (
	*collogs.ExportLogsServiceResponse, error,
) {
	res, err := s.ing.IngestLogs(ctx, convertLogs(req.GetResourceLogs()))
	if err != nil {
		return nil, fmt.Errorf("ingest logs: %w", err)
	}
	resp := &collogs.ExportLogsServiceResponse{}
	if rejected := int64(res.Rejected + res.Dropped); rejected > 0 {
		resp.PartialSuccess = &collogs.ExportLogsPartialSuccess{
			RejectedLogRecords: rejected,
			ErrorMessage:       res.Message,
		}
	}
	return resp, nil
}

func (s *Server) exportMetrics(req *colmetrics.ExportMetricsServiceRequest) *colmetrics.ExportMetricsServiceResponse {
	s.count(trace.SignalMetrics, trace.ResultDiscarded, trace.ReasonNotStored, countDataPoints(req.GetResourceMetrics()))
	return &colmetrics.ExportMetricsServiceResponse{}
}

func partialMessage(msg string, invalid int) string {
	if invalid == 0 {
		return msg
	}
	inv := fmt.Sprintf("%d spans with invalid trace or span id", invalid)
	if msg == "" {
		return inv
	}
	return msg + "; " + inv
}

func isOverloaded(err error) bool { return errors.Is(err, trace.ErrOverloaded) }
