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
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

func TestService_TopSpans(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	tests := []struct {
		name    string
		req     query.TopSpansRequest
		readErr error
		wantErr error
		wantBy  query.TopBy
		wantLim int
	}{
		{name: "defaults", wantBy: query.TopByCost, wantLim: query.DefaultLimit},
		{
			name: "duration", req: query.TopSpansRequest{By: query.TopByDuration, Limit: 5},
			wantBy: query.TopByDuration, wantLim: 5,
		},
		{name: "bad by", req: query.TopSpansRequest{By: "x"}, wantErr: query.ErrInvalidFilter},
		{name: "bad limit", req: query.TopSpansRequest{Limit: 501}, wantErr: query.ErrInvalidLimit},
		{name: "bad range", req: query.TopSpansRequest{From: now, To: now}, wantErr: query.ErrInvalidRange},
		{name: "reader error", readErr: boom, wantErr: boom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := &fakeStats{err: tc.readErr, top: []query.TopSpan{{TraceID: "t"}}}
			svc := newStatsSvc(fs, now)
			got, err := svc.TopSpans(context.Background(), tc.req)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got, 1)
			assert.Equal(t, tc.wantBy, fs.gotReq.By)
			assert.Equal(t, tc.wantLim, fs.gotReq.Limit)
			assert.Equal(t, now, fs.gotReq.To)
			assert.Equal(t, now.Add(-24*time.Hour), fs.gotReq.From)
		})
	}
}

func TestService_IngestStats(t *testing.T) {
	t.Parallel()
	totals := query.IngestStats{Received: 10, Stored: 9}
	tests := []struct {
		name    string
		opts    []queries.Option
		readErr error
		want    query.IngestStats
	}{
		{name: "no gauges", want: totals},
		{
			name: "gauges", opts: []queries.Option{queries.WithGauges(fakeGauges{})},
			want: query.IngestStats{Received: 10, Stored: 9, QueueSpans: 3, QueueBytes: 4, InflightBytes: 5},
		},
		{name: "reader error", readErr: errors.New("boom")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := &fakeStats{err: tc.readErr, totals: totals}
			svc := newStatsSvc(fs, now, tc.opts...)
			got, err := svc.IngestStats(context.Background())
			if tc.readErr != nil {
				require.ErrorIs(t, err, tc.readErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestService_IngestInRange(t *testing.T) {
	t.Parallel()
	totals := query.IngestStats{Received: 10, Rejected: map[string]int64{"queue_full": 2}}
	from, to := now.Add(-time.Hour), now
	fs := &fakeStats{totals: totals}
	svc := newStatsSvc(fs, now, queries.WithGauges(fakeGauges{}))
	got, err := svc.IngestInRange(context.Background(), from, to)
	require.NoError(t, err)
	assert.Equal(t, totals, got, "gauges are not added to a ranged read")
	assert.Equal(t, from, fs.gotFrom)
	assert.Equal(t, to, fs.gotTo)

	_, err = svc.IngestInRange(context.Background(), to, from)
	require.ErrorIs(t, err, query.ErrInvalidRange)

	boom := errors.New("boom")
	_, err = newStatsSvc(&fakeStats{err: boom}, now).IngestInRange(context.Background(), from, to)
	require.ErrorIs(t, err, boom)
}

func newStatsSvc(fs *fakeStats, now time.Time, opts ...queries.Option) *queries.Service {
	r := queries.Readers{Traces: &fakeTraces{}, Sessions: &fakeSessions{}, Rollups: &fakeRollups{}, Stats: fs}
	return queries.New(r, clock.NewFake(now), opts...)
}
