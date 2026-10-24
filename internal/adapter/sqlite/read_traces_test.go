// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func listFixture(t *testing.T) *sqlite.Store {
	t.Helper()
	s := openStore(t)
	for n := 1; n <= 6; n++ {
		start := time.Duration(n) * time.Minute
		root := mkSpan(n, 1, 0, trace.KindChain, at(start), lasting(time.Duration(n)*time.Second),
			session("sess"), user("u"+string(rune('0'+n%2))))
		child := mkSpan(n, 2, 1, trace.KindLLM, at(start+time.Millisecond), model("gpt-a"), usage(1, 1),
			priced(int64(100*(n%3))))
		spans := []trace.Span{root, child}
		if n%2 == 1 {
			spans = append(spans, mkSpan(n, 3, 1, trace.KindLLM, at(start+2*time.Millisecond), model("gpt-b"),
				service("api"), failed(), lasting(6*time.Second)))
		}
		if n == 6 {
			spans = append(spans, mkSpan(n, 4, 1, trace.KindAgent, at(start+3*time.Millisecond), named("agent.plan")))
		}
		writeSpans(t, s, spans...)
	}
	return s
}

func ids(p query.TracePage) []string {
	out := make([]string, 0, len(p.Data))
	for _, d := range p.Data {
		out = append(out, d.TraceID)
	}
	return out
}

func tids(ns ...int) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, tid(n).String())
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func TestStore_ListTraces_Filters(t *testing.T) {
	t.Parallel()
	s := listFixture(t)
	cases := []struct {
		name string
		f    query.TraceFilter
		want []string
	}{
		{name: "all", want: tids(6, 5, 4, 3, 2, 1)},
		{
			name: "range", f: query.TraceFilter{From: anchor.Add(2 * time.Minute), To: anchor.Add(4 * time.Minute)},
			want: tids(3, 2),
		},
		{name: "user", f: query.TraceFilter{UserID: "u1"}, want: tids(5, 3, 1)},
		{name: "session", f: query.TraceFilter{SessionID: "sess"}, want: tids(6, 5, 4, 3, 2, 1)},
		{name: "unknown session", f: query.TraceFilter{SessionID: "nope"}},
		{name: "min cost", f: query.TraceFilter{MinCostNanos: ptr(int64(200))}, want: tids(5, 2)},
		{name: "has error", f: query.TraceFilter{HasError: ptr(true)}, want: tids(5, 3, 1)},
		{name: "no error", f: query.TraceFilter{HasError: ptr(false)}, want: tids(6, 4, 2)},
		{name: "same span: model and status", f: query.TraceFilter{Span: query.SpanFilter{
			Model: "gpt-b", Status: trace.StatusError,
		}}, want: tids(5, 3, 1)},
		{name: "same span: model and other span's status", f: query.TraceFilter{Span: query.SpanFilter{
			Model: "gpt-a", Status: trace.StatusError,
		}}},
		{name: "service and duration", f: query.TraceFilter{Span: query.SpanFilter{
			Service: "api", MinDurationNs: int64(5 * time.Second),
		}}, want: tids(5, 3, 1)},
		{
			name: "duration", f: query.TraceFilter{Span: query.SpanFilter{MinDurationNs: int64(4 * time.Second)}},
			want: tids(6, 5, 4, 3, 1),
		},
		{name: "provider and kind", f: query.TraceFilter{Span: query.SpanFilter{
			Provider: "openai", Kind: trace.KindLLM,
		}}, want: tids(6, 5, 4, 3, 2, 1)},
		{name: "name prefix", f: query.TraceFilter{Span: query.SpanFilter{NamePrefix: "agent."}}, want: tids(6)},
		{name: "name prefix is case-sensitive", f: query.TraceFilter{Span: query.SpanFilter{NamePrefix: "Agent."}}},
		{name: "name prefix of high bytes", f: query.TraceFilter{Span: query.SpanFilter{NamePrefix: "\xff\xff"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := s.ListTraces(context.Background(), tc.f, query.SortStart, query.OrderDesc, query.Page{})
			require.NoError(t, err)
			if tc.want == nil {
				tc.want = []string{}
			}
			assert.Equal(t, tc.want, ids(p))
			assert.Empty(t, p.NextCursor)
		})
	}
}

func TestStore_ListTraces_SortsAndPages(t *testing.T) {
	t.Parallel()
	s := listFixture(t)
	ctx := context.Background()
	cases := []struct {
		name  string
		sort  query.Sort
		order query.Order
		want  []string
	}{
		{name: "start asc", sort: query.SortStart, order: query.OrderAsc, want: tids(1, 2, 3, 4, 5, 6)},
		{name: "cost desc", sort: query.SortCost, order: query.OrderDesc, want: tids(5, 2, 4, 1, 6, 3)},
		{name: "duration desc", sort: query.SortDuration, order: query.OrderDesc, want: tids(5, 3, 1, 6, 4, 2)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			cursor := ""
			for range 10 {
				p, err := s.ListTraces(ctx, query.TraceFilter{}, tc.sort, tc.order, query.Page{Limit: 4, Cursor: cursor})
				require.NoError(t, err)
				got = append(got, ids(p)...)
				if cursor = p.NextCursor; cursor == "" {
					break
				}
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestStore_ListTraces_SortPlans(t *testing.T) {
	t.Parallel()
	s := listFixture(t)
	ctx := context.Background()
	wide := query.TraceFilter{From: anchor.Add(2 * time.Minute), To: anchor.Add(7 * time.Minute)}
	narrow := query.TraceFilter{From: anchor.Add(3 * time.Minute), To: anchor.Add(4 * time.Minute)}
	wideErr := wide
	wideErr.Span = query.SpanFilter{Status: trace.StatusError, MinDurationNs: int64(5 * time.Second)}
	cases := []struct {
		name string
		f    query.TraceFilter
		sort query.Sort
		want []string
	}{
		{name: "wide by cost", f: wide, sort: query.SortCost, want: tids(5, 2, 4, 6, 3)},
		{name: "wide by duration", f: wide, sort: query.SortDuration, want: tids(5, 3, 6, 4, 2)},
		{name: "wide by cost with span filter", f: wideErr, sort: query.SortCost, want: tids(5, 3)},
		{name: "narrow by cost", f: narrow, sort: query.SortCost, want: tids(3)},
		{
			name: "empty range by cost", f: query.TraceFilter{From: anchor.Add(time.Hour), To: anchor.Add(2 * time.Hour)},
			sort: query.SortCost, want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := []string{}
			cursor := ""
			for range 10 {
				p, err := s.ListTraces(ctx, tc.f, tc.sort, query.OrderDesc, query.Page{Limit: 2, Cursor: cursor})
				require.NoError(t, err)
				got = append(got, ids(p)...)
				if cursor = p.NextCursor; cursor == "" {
					break
				}
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestStore_ListTraces_InvalidInput(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	other := query.EncodeCursor(query.Cursor{SortKey: 1, ID: tid(1).String()}, query.SortCost, query.OrderDesc)
	badID := query.EncodeCursor(query.Cursor{SortKey: 1, ID: "zz"}, query.SortStart, query.OrderDesc)
	cases := []struct {
		name  string
		f     query.TraceFilter
		sort  query.Sort
		order query.Order
		page  query.Page
		want  error
	}{
		{
			name: "cursor of another sort", sort: query.SortStart, order: query.OrderDesc,
			page: query.Page{Cursor: other}, want: query.ErrInvalidCursor,
		},
		{
			name: "cursor id", sort: query.SortStart, order: query.OrderDesc, page: query.Page{Cursor: badID},
			want: query.ErrInvalidCursor,
		},
		{
			name: "limit", sort: query.SortStart, order: query.OrderDesc, page: query.Page{Limit: 501},
			want: query.ErrInvalidLimit,
		},
		{name: "sort", sort: "name", order: query.OrderDesc, want: query.ErrInvalidSort},
		{name: "order", sort: query.SortStart, order: "up", want: query.ErrInvalidSort},
		{
			name: "filter", f: query.TraceFilter{Span: query.SpanFilter{Kind: "x"}}, sort: query.SortStart,
			order: query.OrderDesc, want: query.ErrInvalidFilter,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := s.ListTraces(ctx, tc.f, tc.sort, tc.order, tc.page)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestStore_GetTrace_NotFound(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	_, err := s.GetTrace(context.Background(), tid(42), true)
	require.ErrorIs(t, err, trace.ErrNotFound)
}

func TestStore_GetTrace_RootArrivesLast(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	writeSpans(t, s, mkSpan(1, 2, 1, trace.KindLLM, service("worker")))
	d, err := s.GetTrace(ctx, tid(1), false)
	require.NoError(t, err)
	assert.Empty(t, d.Summary.RootName, "root_name stays NULL until the root arrives")
	assert.Equal(t, "worker", d.Summary.Service)

	writeSpans(t, s, mkSpan(1, 1, 0, trace.KindAgent, named("root"), service("front")))
	d, err = s.GetTrace(ctx, tid(1), false)
	require.NoError(t, err)
	assert.Equal(t, "root", d.Summary.RootName)
	assert.Equal(t, "front", d.Summary.Service)
	assert.Equal(t, mkSpan(1, 1, 0, "").StartNs, d.Summary.StartNs)
	assert.Equal(t, sid(1).String(), d.Spans[1].ParentSpanID)
}

func TestStore_TraceModels_CappedAndSorted(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	spans := make([]trace.Span, 0, 11)
	spans = append(spans, mkSpan(1, 1, 0, trace.KindAgent))
	for i, m := range []string{"m9", "m8", "m7", "m6", "m5", "m4", "m3", "m2", "m1", "m1"} {
		spans = append(spans, mkSpan(1, i+2, 1, trace.KindTool, model(m)))
	}
	writeSpans(t, s, spans[:5]...)
	writeSpans(t, s, spans[5:]...)
	d, err := s.GetTrace(context.Background(), tid(1), false)
	require.NoError(t, err)
	assert.Equal(t, []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}, d.Summary.Models)
}
