// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/diagnose"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const (
	noticeFailed  = "The model request failed. Showing the findings only."
	noticeInvalid = "The model did not return a valid answer. Showing the findings only."
)

type Config struct {
	Timeout        time.Duration
	MaxSpans       int
	IncludeContent bool
}

type Option func(*Service)

func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.log = l } }

// Report is the result of one analysis.
type Report struct {
	TraceID      string             `json:"trace_id"`
	Findings     []diagnose.Finding `json:"findings"`
	Causes       []Point            `json:"causes"`
	Fixes        []Point            `json:"fixes"`
	SpansOmitted int                `json:"spans_omitted"`
	Notice       string             `json:"notice"`
}

// Service runs analyses, at most one at a time per trace.
type Service struct {
	traces   TraceReader
	llm      LLM
	cfg      Config
	log      *slog.Logger
	inflight inflight
}

// New returns a Service that reads traces from r and asks llm for an explanation.
func New(r TraceReader, llm LLM, cfg Config, opts ...Option) (*Service, error) {
	if r == nil || llm == nil {
		return nil, fmt.Errorf("%w: nil trace reader or model", ErrInvalidConfig)
	}
	if cfg.Timeout <= 0 || cfg.MaxSpans <= 0 {
		return nil, fmt.Errorf("%w: timeout and max spans must be positive", ErrInvalidConfig)
	}
	s := &Service{traces: r, llm: llm, cfg: cfg, log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func (s *Service) Timeout() time.Duration { return s.cfg.Timeout }

// Analyze computes the findings for a trace and adds the model's causes and fixes when its answer is valid.
func (s *Service) Analyze(ctx context.Context, traceID string) (Report, error) {
	id, err := trace.ParseID(traceID)
	if err != nil {
		return Report{}, err
	}
	if !s.inflight.acquire(id) {
		return Report{}, ErrBusy
	}
	defer s.inflight.release(id)
	d, err := s.traces.GetTrace(ctx, id, s.cfg.IncludeContent)
	if err != nil {
		return Report{}, fmt.Errorf("analyze: get trace: %w", err)
	}
	findings := diagnose.Find(d.Spans)
	rows, omitted := diagnose.Table(d.Spans, findings, s.cfg.MaxSpans)
	rep := Report{
		TraceID: id.String(), Findings: findings, Causes: []Point{}, Fixes: []Point{}, SpansOmitted: omitted,
	}
	if rep.Findings == nil {
		rep.Findings = []diagnose.Finding{}
	}
	callCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	raw, err := s.llm.Complete(callCtx, buildPrompt(d.Summary, findings, rows, omitted))
	if err != nil {
		if ctx.Err() != nil {
			return Report{}, fmt.Errorf("analyze: %w", ctx.Err())
		}
		rep.Notice = noticeFailed
		if errors.Is(err, context.DeadlineExceeded) {
			rep.Notice = fmt.Sprintf("The model did not answer within %s. Showing the findings only.", s.cfg.Timeout)
		}
		s.log.WarnContext(ctx, "model request failed", "component", "analyze", "trace_id", rep.TraceID,
			"error", err.Error())
		return rep, nil
	}
	ans, err := parseAnswer(raw, spanIDs(d.Spans))
	if err != nil {
		rep.Notice = noticeInvalid
		s.log.WarnContext(ctx, "invalid model answer", "component", "analyze", "trace_id", rep.TraceID,
			"error", err.Error())
		return rep, nil
	}
	rep.Causes, rep.Fixes = ans.Causes, ans.Fixes
	return rep, nil
}

func spanIDs(spans []query.SpanView) map[string]bool {
	ids := make(map[string]bool, len(spans))
	for i := range spans {
		ids[spans[i].SpanID] = true
	}
	return ids
}
