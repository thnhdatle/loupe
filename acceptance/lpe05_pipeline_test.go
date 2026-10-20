//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// lpe05SpanDuration is the duration given to every fixture span.
const lpe05SpanDuration = time.Millisecond

// lpe05Operations maps a fixture span kind to the gen_ai.operation.name that
// semconv resolves to it; kind other has no operation (no model, no usage).
var lpe05Operations = map[trace.Kind]string{
	trace.KindLLM:       "chat",
	trace.KindEmbedding: "embeddings",
	trace.KindAgent:     "invoke_agent",
	trace.KindChain:     "invoke_workflow",
	trace.KindTool:      "execute_tool",
	trace.KindRetrieval: "retrieval",
	trace.KindOther:     "",
}

// lpe05Pipeline is the production write and read path: OTLP-shaped raw spans
// go through app/ingest (semconv translation, cost.Calculator) and app/writer
// into the SQLite store (usage dedupe and cost transfer at settle), and are
// read back through app/queries over the store's TraceReader.
type lpe05Pipeline struct {
	ingestor *ingest.Ingestor
	queries  *queries.Service
	resource *trace.RawResource
	stop     func() error
}

func startLPE05Pipeline(ctx context.Context, dir string, calc *cost.Calculator) (*lpe05Pipeline, error) {
	store, err := sqlite.Open(ctx, filepath.Join(dir, "loupe.db"))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	clk := clock.System{}
	w, err := writer.New(store, clk, writer.WithBatchInterval(2*time.Millisecond))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("writer: %w", err), store.Close())
	}
	ing, err := ingest.New(w, calc, clk, ingest.WithSyncAck(true))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("ingest: %w", err), store.Close())
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	return &lpe05Pipeline{
		ingestor: ing,
		queries:  queries.New(queries.Readers{Traces: store, Sessions: store, Rollups: store, Stats: store}, clk),
		resource: &trace.RawResource{Attributes: []trace.Attr{
			{Key: "service.name", Value: trace.StringValue("lpe05-cost")},
		}},
		stop: func() error {
			cancel()
			return errors.Join(<-done, store.Close())
		},
	}, nil
}

// ingest sends one request's spans with sync ack, so they are committed (and
// the trace settled) when it returns.
func (p *lpe05Pipeline) ingest(ctx context.Context, raws []trace.RawSpan) error {
	res, err := p.ingestor.IngestSpans(ctx, raws)
	if err != nil {
		return fmt.Errorf("ingest: %w", err)
	}
	if res.Accepted != len(raws) {
		return fmt.Errorf("ingest: accepted %d of %d spans (%s)", res.Accepted, len(raws), res.Message)
	}
	return nil
}

func (p *lpe05Pipeline) trace(ctx context.Context, id trace.ID) (query.TraceDetail, error) {
	d, err := p.queries.GetTrace(ctx, id.String(), false)
	if err != nil {
		return query.TraceDetail{}, fmt.Errorf("get trace %s: %w", id, err)
	}
	return d, nil
}

// lpe05TraceID is the trace id of fixture case i.
func lpe05TraceID(i int) trace.ID {
	return trace.ID{0x4c, 0x50, 0x45, 0x05, 14: byte(i >> 8), 15: byte(i)}
}

// lpe05Raw builds an OTLP-shaped span carrying the case data as gen_ai
// attributes; empty strings are left out, as an instrumentation would.
type lpe05Raw struct {
	traceID                trace.ID
	spanID, parentID       trace.SpanID
	operation              string
	provider, model, reqMd string
	start                  lpe05Text
	usage                  *trace.Usage // nil: no usage attributes
}

func (p *lpe05Pipeline) raw(r *lpe05Raw) (trace.RawSpan, error) {
	start, err := time.Parse(time.RFC3339Nano, string(r.start))
	if err != nil {
		return trace.RawSpan{}, fmt.Errorf("start %q: %w", r.start, err)
	}
	var attrs []trace.Attr
	str := func(k, v string) {
		if v != "" {
			attrs = append(attrs, trace.Attr{Key: k, Value: trace.StringValue(v)})
		}
	}
	str("gen_ai.operation.name", r.operation)
	str("gen_ai.provider.name", r.provider)
	str("gen_ai.response.model", r.model)
	str("gen_ai.request.model", r.reqMd)
	if u := r.usage; u != nil {
		for _, kv := range []struct {
			k string
			n int64
		}{
			{"gen_ai.usage.input_tokens", u.Input},
			{"gen_ai.usage.output_tokens", u.Output},
			{"gen_ai.usage.cache_read.input_tokens", u.CacheRead},
			{"gen_ai.usage.cache_write.input_tokens", u.CacheWrite},
			{"gen_ai.usage.reasoning.output_tokens", u.Reasoning},
		} {
			attrs = append(attrs, trace.Attr{Key: kv.k, Value: trace.IntValue(kv.n)})
		}
	}
	name := r.operation
	if r.model != "" {
		name += " " + r.model
	}
	return trace.RawSpan{
		Resource: p.resource, TraceID: r.traceID, SpanID: r.spanID, ParentSpanID: r.parentID,
		Name: name, SpanKind: 3, StartNs: start.UnixNano(), EndNs: start.Add(lpe05SpanDuration).UnixNano(),
		StatusCode: trace.StatusCodeOK, Attributes: attrs,
	}, nil
}

// spanCaseRaws returns the single llm span of a span case.
func (p *lpe05Pipeline) spanCaseRaws(c *lpe05Case, id trace.ID) ([]trace.RawSpan, error) {
	u := c.Usage.usage()
	raw, err := p.raw(&lpe05Raw{
		traceID: id, spanID: trace.SpanID{7: 1}, operation: lpe05Operations[trace.KindLLM],
		provider: c.Provider, model: c.Model, reqMd: c.RequestModel, start: c.Start, usage: &u,
	})
	if err != nil {
		return nil, err
	}
	return []trace.RawSpan{raw}, nil
}

// traceCaseRaws returns the spans of a trace case.
func (p *lpe05Pipeline) traceCaseRaws(c *lpe05Case, id trace.ID) ([]trace.RawSpan, error) {
	raws := make([]trace.RawSpan, 0, len(c.Spans))
	for i := range c.Spans {
		s := &c.Spans[i]
		op, ok := lpe05Operations[s.Kind]
		if !ok {
			return nil, fmt.Errorf("span %s: unsupported kind %q", s.ID, s.Kind)
		}
		r := lpe05Raw{traceID: id, operation: op, provider: s.Provider, model: s.Model, start: s.Start}
		var err error
		if r.spanID, err = trace.ParseSpanID(s.ID); err != nil {
			return nil, fmt.Errorf("span id %q: %w", s.ID, err)
		}
		if s.Parent != "" {
			if r.parentID, err = trace.ParseSpanID(s.Parent); err != nil {
				return nil, fmt.Errorf("parent id %q: %w", s.Parent, err)
			}
		}
		if u := s.Usage.usage(); s.Kind.HasUsageDedupe() || !u.IsZero() {
			r.usage = &u
		}
		raw, err := p.raw(&r)
		if err != nil {
			return nil, fmt.Errorf("span %s: %w", s.ID, err)
		}
		raws = append(raws, raw)
	}
	return raws, nil
}
