// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const DefaultMaxAttributeBytes = 64 << 10

const (
	msgQueueFullRejected = "ingest queue full; retry later"
	msgQueueFullDropped  = "ingest queue full; spans dropped"
	msgStoreError        = "store error; spans dropped"
	msgShuttingDown      = "shutting down"
)

var (
	ErrInvalidConfig   = errors.New("ingest: invalid configuration")
	ErrInvalidOverflow = errors.New("ingest: invalid overflow mode")
)

type Overflow string

const (
	OverflowReject Overflow = "reject"
	OverflowDrop   Overflow = "drop"
)

func ParseOverflow(s string) (Overflow, error) {
	switch o := Overflow(s); o {
	case OverflowReject, OverflowDrop:
		return o, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidOverflow, s)
	}
}

type Queue interface {
	Enqueue(b *trace.Batch, slots int, bytes int64, wait bool) (<-chan error, error)
	AddCount(c trace.IngestCount)
	HasRoom(slots int) bool
}

type config struct {
	overflow     Overflow
	syncAck      bool
	policy       content.Policy
	maxAttrBytes int
	logger       *slog.Logger
	onUnpriced   func(model string, n int64)
}

type Option func(*config)

func WithOverflow(o Overflow) Option { return func(c *config) { c.overflow = o } }

func WithSyncAck(on bool) Option { return func(c *config) { c.syncAck = on } }

func WithContentPolicy(p content.Policy) Option { return func(c *config) { c.policy = p } }

func WithMaxAttributeBytes(n int) Option { return func(c *config) { c.maxAttrBytes = n } }

func WithLogger(l *slog.Logger) Option { return func(c *config) { c.logger = l } }

func WithUnpricedHook(f func(model string, n int64)) Option {
	return func(c *config) { c.onUnpriced = f }
}

// Ingestor translates, filters, prices and enqueues ingest requests.
type Ingestor struct {
	q     Queue
	calc  *cost.Calculator
	clk   clock.Clock
	cfg   config
	trOpt semconv.Options
	log   *slog.Logger
}

var _ trace.Ingestor = (*Ingestor)(nil)

// New returns an Ingestor that feeds q and prices with calc.
func New(q Queue, calc *cost.Calculator, clk clock.Clock, opts ...Option) (*Ingestor, error) {
	if q == nil || calc == nil || clk == nil {
		return nil, fmt.Errorf("%w: queue, calculator and clock are required", ErrInvalidConfig)
	}
	cfg := config{
		overflow:     OverflowReject,
		policy:       content.Policy{Mode: content.ModeOff},
		maxAttrBytes: DefaultMaxAttributeBytes,
		logger:       slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		o(&cfg)
	}
	var errs []error
	if _, err := ParseOverflow(string(cfg.overflow)); err != nil {
		errs = append(errs, err)
	}
	if _, err := content.ParseMode(string(cfg.policy.Mode)); err != nil {
		errs = append(errs, fmt.Errorf("%w: %w", ErrInvalidConfig, err))
	}
	if cfg.maxAttrBytes < 0 {
		errs = append(errs, fmt.Errorf("%w: max attribute bytes must not be negative", ErrInvalidConfig))
	}
	if cfg.logger == nil {
		errs = append(errs, fmt.Errorf("%w: logger must not be nil", ErrInvalidConfig))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	in := &Ingestor{q: q, calc: calc, clk: clk, cfg: cfg, log: cfg.logger.With("component", "ingest")}
	in.trOpt = semconv.Options{MaxAttrBytes: cfg.maxAttrBytes}
	if cfg.policy.Redactor != nil {
		in.trOpt.RedactValue = cfg.policy.Redactor.Redact
	}
	return in, nil
}

// IngestSpans translates, filters, prices and enqueues the spans of one request.
func (in *Ingestor) IngestSpans(ctx context.Context, raws []trace.RawSpan) (trace.IngestResult, error) {
	if len(raws) == 0 {
		return trace.IngestResult{}, nil
	}
	if err := ctx.Err(); err != nil {
		return trace.IngestResult{}, err
	}
	now := in.clk.Now().UnixNano()
	if !in.q.HasRoom(len(raws)) {
		in.add(now, trace.SignalTraces, trace.ResultReceived, "", int64(len(raws)))
		return in.overflow(now, trace.SignalTraces, len(raws))
	}
	b := &trace.Batch{Spans: make([]trace.Span, 0, len(raws))}
	var (
		bytes              int64
		stripped, unpriced int64
		byModel            map[string]int64
	)
	seen := make(map[*trace.RawResource]struct{})
	for i := range raws {
		if r := raws[i].Resource; r != nil {
			if _, ok := seen[r]; !ok {
				seen[r] = struct{}{}
				b.Resources = appendResource(b.Resources, semconv.CanonicalResource(r))
			}
		}
		s := in.span(&raws[i])
		if s.ContentStripped {
			stripped++
		}
		if !s.Cost.Priced && !s.Usage.IsZero() {
			unpriced++
			byModel = in.tallyUnpriced(byModel, s.Model)
		}
		bytes += EstimateSpanBytes(&s)
		b.Spans = append(b.Spans, s)
	}
	for i := range b.Resources {
		bytes += EstimateResourceBytes(&b.Resources[i])
	}
	in.add(now, trace.SignalTraces, trace.ResultReceived, "", int64(len(raws)))
	in.add(now, trace.SignalTraces, trace.ResultContentStripped, "", stripped)
	in.add(now, trace.SignalTraces, trace.ResultUnpriced, "", unpriced)
	for m, n := range byModel {
		in.cfg.onUnpriced(m, n)
	}
	return in.enqueue(ctx, request{batch: b, n: len(raws), bytes: bytes, signal: trace.SignalTraces, now: now})
}

func (in *Ingestor) tallyUnpriced(by map[string]int64, model string) map[string]int64 {
	if in.cfg.onUnpriced == nil {
		return by
	}
	if by == nil {
		by = make(map[string]int64)
	}
	by[model]++
	return by
}

func appendResource(rs []trace.Resource, r trace.Resource) []trace.Resource {
	if slices.ContainsFunc(rs, func(x trace.Resource) bool { return x.ID == r.ID }) {
		return rs
	}
	return append(rs, r)
}

func (in *Ingestor) span(raw *trace.RawSpan) trace.Span {
	s := semconv.Translate(raw, in.trOpt)
	s.Content, s.ContentStripped = content.Apply(in.cfg.policy, s.Content)
	res, err := in.calc.Cost(cost.Input{
		Provider:      s.Provider,
		ResponseModel: s.Model,
		Start:         time.Unix(0, s.StartNs).UTC(),
		Usage:         s.Usage,
	})
	if err != nil {
		in.log.Debug("span not priced", "trace_id", s.TraceID.String(), "span_id", s.SpanID.String(), "error", err)
		return s
	}
	s.Cost = res.Cost
	s.Flags |= res.Flags
	return s
}

// IngestLogs turns GenAI log events into content parts and enqueues them.
func (in *Ingestor) IngestLogs(ctx context.Context, recs []trace.RawLogRecord) (trace.IngestResult, error) {
	if len(recs) == 0 {
		return trace.IngestResult{}, nil
	}
	if err := ctx.Err(); err != nil {
		return trace.IngestResult{}, err
	}
	now := in.clk.Now().UnixNano()
	b := &trace.Batch{}
	var (
		bytes            int64
		kept, stripped   int
		noSpan, notGenAI int64
	)
	for i := range recs {
		parts, reason, keep := semconv.TranslateLog(&recs[i])
		if !keep {
			if reason == trace.ReasonNoSpanContext {
				noSpan++
			} else {
				notGenAI++
			}
			continue
		}
		parts, st := content.Apply(in.cfg.policy, parts)
		if st {
			stripped++
		}
		for j := range parts {
			bytes += EstimatePartBytes(&parts[j])
		}
		b.Content = append(b.Content, parts...)
		kept++
	}
	in.add(now, trace.SignalLogs, trace.ResultReceived, "", int64(len(recs)))
	in.add(now, trace.SignalLogs, trace.ResultDiscarded, trace.ReasonNoSpanContext, noSpan)
	in.add(now, trace.SignalLogs, trace.ResultDiscarded, trace.ReasonNotGenAI, notGenAI)
	in.add(now, trace.SignalLogs, trace.ResultContentStripped, "", int64(stripped))
	discarded := len(recs) - kept
	if kept == 0 {
		return trace.IngestResult{Accepted: discarded}, nil
	}
	res, err := in.enqueue(ctx, request{batch: b, n: kept, bytes: bytes, signal: trace.SignalLogs, now: now})
	if err == nil {
		res.Accepted += discarded
	}
	return res, err
}

func (in *Ingestor) Count(c trace.IngestCount) {
	if c.AtNs == 0 {
		c.AtNs = in.clk.Now().UnixNano()
	}
	in.q.AddCount(c)
}

func (in *Ingestor) add(now int64, sig trace.Signal, res trace.Result, reason string, n int64) {
	if n > 0 {
		in.q.AddCount(trace.IngestCount{AtNs: now, Signal: sig, Result: res, Reason: reason, N: n})
	}
}

type request struct {
	batch  *trace.Batch
	n      int
	bytes  int64
	signal trace.Signal
	now    int64
}

func (in *Ingestor) enqueue(ctx context.Context, r request) (trace.IngestResult, error) {
	n, sig, now := r.n, r.signal, r.now
	ack, err := in.q.Enqueue(r.batch, n, r.bytes, in.cfg.syncAck)
	switch {
	case errors.Is(err, trace.ErrOverloaded):
		return in.overflow(now, sig, n)
	case errors.Is(err, trace.ErrShuttingDown):
		in.add(now, sig, trace.ResultRejected, trace.ReasonShutdown, int64(n))
		return trace.IngestResult{Rejected: n, Message: msgShuttingDown}, trace.ErrShuttingDown
	case err != nil:
		return trace.IngestResult{}, fmt.Errorf("ingest: enqueue: %w", err)
	case ack == nil:
		return trace.IngestResult{Accepted: n}, nil
	}
	select {
	case err := <-ack:
		switch {
		case err == nil:
			return trace.IngestResult{Accepted: n}, nil
		case errors.Is(err, trace.ErrShuttingDown):
			return trace.IngestResult{Rejected: n, Message: msgShuttingDown}, trace.ErrShuttingDown
		default:
			return trace.IngestResult{Dropped: n, Message: msgStoreError}, nil
		}
	case <-ctx.Done():
		return trace.IngestResult{}, fmt.Errorf("ingest: wait for commit: %w", ctx.Err())
	}
}

func (in *Ingestor) overflow(now int64, sig trace.Signal, n int) (trace.IngestResult, error) {
	if in.cfg.overflow == OverflowDrop {
		in.add(now, sig, trace.ResultDropped, trace.ReasonQueueFull, int64(n))
		return trace.IngestResult{Dropped: n, Message: msgQueueFullDropped}, nil
	}
	in.add(now, sig, trace.ResultRejected, trace.ReasonQueueFull, int64(n))
	return trace.IngestResult{Rejected: n, Message: msgQueueFullRejected}, trace.ErrOverloaded
}
