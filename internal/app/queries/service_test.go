// SPDX-License-Identifier: Apache-2.0

package queries_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

var now = time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)

const validID = "0123456789abcdef0123456789abcdef"

func newSvc(tr *fakeTraces, se *fakeSessions, ro *fakeRollups) *queries.Service {
	return queries.New(queries.Readers{Traces: tr, Sessions: se, Rollups: ro, Stats: &fakeStats{}}, clock.NewFake(now))
}

func TestService_ListTraces(t *testing.T) {
	t.Parallel()
	neg := int64(-1)
	from := now.Add(-time.Hour)
	boom := errors.New("boom")
	tests := []struct {
		name    string
		req     queries.TraceListRequest
		readErr error
		wantErr error
		check   func(t *testing.T, tr *fakeTraces)
	}{
		{name: "defaults", req: queries.TraceListRequest{}, check: func(t *testing.T, tr *fakeTraces) {
			t.Helper()
			assert.Equal(t, now, tr.gotF.To)
			assert.Equal(t, now.Add(-24*time.Hour), tr.gotF.From)
			assert.Equal(t, query.SortStart, tr.gotSort)
			assert.Equal(t, query.OrderDesc, tr.gotOrder)
			assert.Equal(t, query.Page{Limit: query.DefaultLimit}, tr.gotPage)
		}},
		{name: "explicit", req: queries.TraceListRequest{
			Filter: query.TraceFilter{From: from, UserID: "u"}, Sort: query.SortCost, Order: query.OrderAsc,
			Page: query.Page{Limit: 7, Cursor: "c"},
		}, check: func(t *testing.T, tr *fakeTraces) {
			t.Helper()
			assert.Equal(t, from, tr.gotF.From)
			assert.Equal(t, "u", tr.gotF.UserID)
			assert.Equal(t, query.SortCost, tr.gotSort)
			assert.Equal(t, query.OrderAsc, tr.gotOrder)
			assert.Equal(t, query.Page{Limit: 7, Cursor: "c"}, tr.gotPage)
		}},
		{
			name:    "from after to",
			req:     queries.TraceListRequest{Filter: query.TraceFilter{From: now, To: from}},
			wantErr: query.ErrInvalidRange,
		},
		{
			name:    "bad filter",
			req:     queries.TraceListRequest{Filter: query.TraceFilter{MinCostNanos: &neg}},
			wantErr: query.ErrInvalidFilter,
		},
		{name: "bad sort", req: queries.TraceListRequest{Sort: "x"}, wantErr: query.ErrInvalidSort},
		{name: "bad order", req: queries.TraceListRequest{Order: "x"}, wantErr: query.ErrInvalidSort},
		{
			name:    "limit too big",
			req:     queries.TraceListRequest{Page: query.Page{Limit: 501}},
			wantErr: query.ErrInvalidLimit,
		},
		{name: "reader error", req: queries.TraceListRequest{}, readErr: boom, wantErr: boom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := &fakeTraces{err: tc.readErr, page: query.TracePage{NextCursor: "n"}}
			page, err := newSvc(tr, &fakeSessions{}, &fakeRollups{}).ListTraces(context.Background(), tc.req)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "n", page.NextCursor)
			tc.check(t, tr)
		})
	}
}

func TestService_GetTrace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		id      string
		readErr error
		wantErr error
	}{
		{name: "ok", id: validID},
		{name: "bad id", id: "zz", wantErr: trace.ErrInvalidID},
		{name: "not found", id: validID, readErr: trace.ErrNotFound, wantErr: trace.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := &fakeTraces{err: tc.readErr, detail: query.TraceDetail{Summary: query.TraceSummary{TraceID: validID}}}
			d, err := newSvc(tr, &fakeSessions{}, &fakeRollups{}).GetTrace(context.Background(), tc.id, true)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, validID, d.Summary.TraceID)
			assert.Equal(t, validID, tr.gotID.String())
			assert.True(t, tr.gotInc)
		})
	}
}

func TestService_ListSessions(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	tests := []struct {
		name    string
		req     queries.SessionListRequest
		readErr error
		wantErr error
	}{
		{name: "defaults", req: queries.SessionListRequest{Filter: query.SessionFilter{UserID: "u"}}},
		{
			name:    "bad range",
			req:     queries.SessionListRequest{Filter: query.SessionFilter{From: now, To: now}},
			wantErr: query.ErrInvalidRange,
		},
		{
			name:    "bad limit",
			req:     queries.SessionListRequest{Page: query.Page{Limit: -1}},
			wantErr: query.ErrInvalidLimit,
		},
		{name: "reader error", readErr: boom, wantErr: boom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			se := &fakeSessions{err: tc.readErr, page: query.SessionPage{NextCursor: "n"}}
			page, err := newSvc(&fakeTraces{}, se, &fakeRollups{}).ListSessions(context.Background(), tc.req)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "n", page.NextCursor)
			assert.Equal(t, now, se.gotF.To)
			assert.Equal(t, now.Add(-24*time.Hour), se.gotF.From)
			assert.Equal(t, query.DefaultLimit, se.gotPage.Limit)
		})
	}
}

func TestService_GetSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		id      string
		page    query.Page
		readErr error
		wantErr error
	}{
		{name: "ok", id: "s1", page: query.Page{Limit: 200}},
		{name: "empty id", id: "", wantErr: query.ErrInvalidFilter},
		{name: "bad limit", id: "s1", page: query.Page{Limit: 9999}, wantErr: query.ErrInvalidLimit},
		{name: "not found", id: "s1", readErr: trace.ErrNotFound, wantErr: trace.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			se := &fakeSessions{err: tc.readErr, detail: query.SessionDetail{Summary: query.SessionSummary{ID: "s1"}}}
			d, err := newSvc(&fakeTraces{}, se, &fakeRollups{}).GetSession(context.Background(), tc.id, tc.page)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "s1", d.Summary.ID)
			assert.Equal(t, "s1", se.gotID)
			assert.Equal(t, 200, se.gotPage.Limit)
		})
	}
}
