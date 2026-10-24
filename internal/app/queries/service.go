// SPDX-License-Identifier: Apache-2.0

package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// Service implements the query use cases.
type Service struct {
	traces   query.TraceReader
	sessions query.SessionReader
	rollups  rollup.RollupReader
	stats    query.StatsReader
	clock    clock.Clock
	gauges   Gauges
}

type Gauges interface {
	QueueDepth() (spans, bytes int64)
	InflightBytes() int64
}

type Option func(*Service)

func WithGauges(g Gauges) Option { return func(s *Service) { s.gauges = g } }

type Readers struct {
	Traces   query.TraceReader
	Sessions query.SessionReader
	Rollups  rollup.RollupReader
	Stats    query.StatsReader
}

// New returns a Service that reads from r.
func New(r Readers, clk clock.Clock, opts ...Option) *Service {
	s := &Service{traces: r.Traces, sessions: r.Sessions, rollups: r.Rollups, stats: r.Stats, clock: clk}
	for _, o := range opts {
		o(s)
	}
	return s
}

type TraceListRequest struct {
	Filter query.TraceFilter
	Sort   query.Sort
	Order  query.Order
	Page   query.Page
}

// ListTraces returns one page of trace summaries.
func (s *Service) ListTraces(ctx context.Context, req TraceListRequest) (query.TracePage, error) {
	f := req.Filter
	var err error
	if f.From, f.To, err = s.resolve(f.From, f.To); err != nil {
		return query.TracePage{}, err
	}
	if err = f.Validate(); err != nil {
		return query.TracePage{}, err
	}
	sort, err := query.ParseSort(string(req.Sort))
	if err != nil {
		return query.TracePage{}, err
	}
	order, err := query.ParseOrder(string(req.Order))
	if err != nil {
		return query.TracePage{}, err
	}
	pg, err := normalizePage(req.Page)
	if err != nil {
		return query.TracePage{}, err
	}
	page, err := s.traces.ListTraces(ctx, f, sort, order, pg)
	if err != nil {
		return query.TracePage{}, fmt.Errorf("queries: list traces: %w", err)
	}
	return page, nil
}

// GetTrace returns one trace with its spans.
func (s *Service) GetTrace(ctx context.Context, id string, includeContent bool) (query.TraceDetail, error) {
	tid, err := trace.ParseID(id)
	if err != nil {
		return query.TraceDetail{}, err
	}
	d, err := s.traces.GetTrace(ctx, tid, includeContent)
	if err != nil {
		return query.TraceDetail{}, fmt.Errorf("queries: get trace: %w", err)
	}
	return d, nil
}

type SessionListRequest struct {
	Filter query.SessionFilter
	Page   query.Page
}

func (s *Service) ListSessions(ctx context.Context, req SessionListRequest) (query.SessionPage, error) {
	f := req.Filter
	var err error
	if f.From, f.To, err = s.resolve(f.From, f.To); err != nil {
		return query.SessionPage{}, err
	}
	pg, err := normalizePage(req.Page)
	if err != nil {
		return query.SessionPage{}, err
	}
	page, err := s.sessions.ListSessions(ctx, f, pg)
	if err != nil {
		return query.SessionPage{}, fmt.Errorf("queries: list sessions: %w", err)
	}
	return page, nil
}

func (s *Service) GetSession(ctx context.Context, id string, p query.Page) (query.SessionDetail, error) {
	if id == "" {
		return query.SessionDetail{}, fmt.Errorf("%w: empty session id", query.ErrInvalidFilter)
	}
	pg, err := normalizePage(p)
	if err != nil {
		return query.SessionDetail{}, err
	}
	d, err := s.sessions.GetSession(ctx, id, pg)
	if err != nil {
		return query.SessionDetail{}, fmt.Errorf("queries: get session: %w", err)
	}
	return d, nil
}

func (s *Service) resolve(from, to time.Time) (time.Time, time.Time, error) {
	var fp, tp *time.Time
	if !from.IsZero() {
		fp = &from
	}
	if !to.IsZero() {
		tp = &to
	}
	return query.ResolveRange(fp, tp, s.clock.Now())
}

func normalizePage(p query.Page) (query.Page, error) {
	n, err := query.NormalizeLimit(p.Limit)
	if err != nil {
		return query.Page{}, err
	}
	return query.Page{Limit: n, Cursor: p.Cursor}, nil
}

func (s *Service) TopSpans(ctx context.Context, req query.TopSpansRequest) ([]query.TopSpan, error) {
	var err error
	if req.From, req.To, err = s.resolve(req.From, req.To); err != nil {
		return nil, err
	}
	if err = req.Validate(); err != nil {
		return nil, err
	}
	by, err := query.ParseTopBy(string(req.By))
	if err != nil {
		return nil, err
	}
	req.By = by
	if req.Limit, err = query.NormalizeLimit(req.Limit); err != nil {
		return nil, err
	}
	out, err := s.stats.TopSpans(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("queries: top spans: %w", err)
	}
	return out, nil
}

func (s *Service) IngestStats(ctx context.Context) (query.IngestStats, error) {
	st, err := s.stats.IngestTotals(ctx)
	if err != nil {
		return query.IngestStats{}, fmt.Errorf("queries: ingest stats: %w", err)
	}
	if s.gauges != nil {
		st.QueueSpans, st.QueueBytes = s.gauges.QueueDepth()
		st.InflightBytes = s.gauges.InflightBytes()
	}
	return st, nil
}

func (s *Service) IngestInRange(ctx context.Context, from, to time.Time) (query.IngestStats, error) {
	if !to.After(from) {
		return query.IngestStats{}, fmt.Errorf("queries: ingest in range: %w", query.ErrInvalidRange)
	}
	st, err := s.stats.IngestTotalsBetween(ctx, from, to)
	if err != nil {
		return query.IngestStats{}, fmt.Errorf("queries: ingest in range: %w", err)
	}
	return st, nil
}
