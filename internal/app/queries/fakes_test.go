// SPDX-License-Identifier: Apache-2.0

package queries_test

import (
	"context"
	"sync"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type fakeTraces struct {
	mu       sync.Mutex
	err      error
	page     query.TracePage
	detail   query.TraceDetail
	gotF     query.TraceFilter
	gotSort  query.Sort
	gotOrder query.Order
	gotPage  query.Page
	gotID    trace.ID
	gotInc   bool
}

func (f *fakeTraces) ListTraces(
	_ context.Context, tf query.TraceFilter, s query.Sort, o query.Order, p query.Page,
) (query.TracePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotF, f.gotSort, f.gotOrder, f.gotPage = tf, s, o, p
	return f.page, f.err
}

func (f *fakeTraces) GetTrace(_ context.Context, id trace.ID, inc bool) (query.TraceDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotID, f.gotInc = id, inc
	return f.detail, f.err
}

type fakeSessions struct {
	mu      sync.Mutex
	err     error
	page    query.SessionPage
	detail  query.SessionDetail
	gotF    query.SessionFilter
	gotPage query.Page
	gotID   string
}

func (f *fakeSessions) ListSessions(
	_ context.Context, sf query.SessionFilter, p query.Page,
) (query.SessionPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotF, f.gotPage = sf, p
	return f.page, f.err
}

func (f *fakeSessions) GetSession(_ context.Context, id string, p query.Page) (query.SessionDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotID, f.gotPage = id, p
	return f.detail, f.err
}

type readCall struct {
	Res rollup.Resolution
	R   rollup.Range
}

// fakeRollups serves rows like the storage adapter: only rows of the
// requested resolution whose bucket lies in the range (and kind, if set).
type fakeRollups struct {
	mu        sync.Mutex
	hour, day []rollup.Row
	uHour     []rollup.UserRow
	uDay      []rollup.UserRow
	failOn    rollup.Resolution // 0: never fail
	err       error
	calls     []readCall
}

func (f *fakeRollups) ReadRollups(
	_ context.Context, res rollup.Resolution, r rollup.Range, flt rollup.Filter,
) ([]rollup.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, readCall{res, r})
	if f.err != nil && (f.failOn == 0 || f.failOn == res) {
		return nil, f.err
	}
	src := f.hour
	if res == rollup.ResolutionDay {
		src = f.day
	}
	var out []rollup.Row
	for i := range src {
		if r.Contains(src[i].Key.BucketNs) && (flt.Kind == "" || flt.Kind == src[i].Key.Kind) {
			out = append(out, src[i])
		}
	}
	return out, nil
}

func (f *fakeRollups) ReadUserRollups(
	_ context.Context, res rollup.Resolution, r rollup.Range,
) ([]rollup.UserRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, readCall{res, r})
	if f.err != nil && (f.failOn == 0 || f.failOn == res) {
		return nil, f.err
	}
	src := f.uHour
	if res == rollup.ResolutionDay {
		src = f.uDay
	}
	var out []rollup.UserRow
	for i := range src {
		if r.Contains(src[i].Key.BucketNs) {
			out = append(out, src[i])
		}
	}
	return out, nil
}

type fakeStats struct {
	err    error
	top    []query.TopSpan
	totals query.IngestStats
	gotReq query.TopSpansRequest
	// range read by IngestTotalsBetween
	gotFrom, gotTo time.Time
}

func (f *fakeStats) TopSpans(_ context.Context, r query.TopSpansRequest) ([]query.TopSpan, error) {
	f.gotReq = r
	return f.top, f.err
}

func (f *fakeStats) IngestTotals(context.Context) (query.IngestStats, error) {
	return f.totals, f.err
}

func (f *fakeStats) IngestTotalsBetween(_ context.Context, from, to time.Time) (query.IngestStats, error) {
	f.gotFrom, f.gotTo = from, to
	return f.totals, f.err
}

type fakeGauges struct{}

func (fakeGauges) QueueDepth() (int64, int64) { return 3, 4 }
func (fakeGauges) InflightBytes() int64       { return 5 }
