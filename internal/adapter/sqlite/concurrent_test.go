// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// readAll runs one round of every read the dashboard and API make.
func readAll(ctx context.Context, s *sqlite.Store, n int) error {
	var errs []error
	note := func(what string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", what, err))
		}
	}
	day := rollup.Range{FromNs: anchor.UnixNano(), ToNs: anchor.Add(24 * time.Hour).UnixNano()}
	page, err := s.ListTraces(ctx, query.TraceFilter{Span: query.SpanFilter{Kind: trace.KindLLM}},
		query.SortCost, query.OrderDesc, query.Page{Limit: 10})
	note("list traces", err)
	for _, tr := range page.Data {
		id, perr := trace.ParseID(tr.TraceID)
		note("parse id", perr)
		_, gerr := s.GetTrace(ctx, id, n%2 == 0)
		note("get trace", gerr)
	}
	_, err = s.ListSessions(ctx, query.SessionFilter{}, query.Page{})
	note("list sessions", err)
	if _, err = s.GetSession(ctx, "s", query.Page{Limit: 5}); !errors.Is(err, trace.ErrNotFound) {
		note("get session", err)
	}
	_, err = s.TopSpans(ctx, query.TopSpansRequest{From: anchor, To: anchor.Add(time.Hour)})
	note("top spans", err)
	_, err = s.IngestTotals(ctx)
	note("ingest totals", err)
	_, err = s.ReadRollups(ctx, rollup.ResolutionHour, day, rollup.Filter{})
	note("rollups", err)
	_, err = s.ReadUserRollups(ctx, rollup.ResolutionDay, day)
	note("user rollups", err)
	return errors.Join(errs...)
}

// Concurrent ingest and concurrent dashboard queries (-race).
func TestStore_ConcurrentReadersDuringWrites(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	const batches = 40
	var done atomic.Bool
	var reads atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for r := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; !done.Load(); i++ {
				if err := readAll(ctx, s, r+i); err != nil {
					errs <- err
					return
				}
				reads.Add(1)
			}
		}()
	}
	for b := range batches {
		spans := triageTrace(b + 1)
		for i := range spans {
			spans[i].SessionID = "s"
		}
		writeSpans(t, s, append(spans, simpleTrace(1000+b)...)...)
	}
	done.Store(true)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Positive(t, reads.Load())
	d, err := s.GetSession(ctx, "s", query.Page{})
	require.NoError(t, err)
	assert.Equal(t, batches, d.Summary.TraceCount)
	assert.Equal(t, int64(batches*1234), d.Summary.CostNanos)
}
