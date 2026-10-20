// SPDX-License-Identifier: Apache-2.0

package query_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestParseGroupBy(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "model", "provider", "service", "kind", "user"} {
		g, err := query.ParseGroupBy(in)
		require.NoError(t, err)
		assert.Equal(t, query.GroupBy(in), g)
	}
	_, err := query.ParseGroupBy("team")
	require.ErrorIs(t, err, query.ErrInvalidFilter)
}

func TestParseInterval(t *testing.T) {
	t.Parallel()
	i, err := query.ParseInterval("")
	require.NoError(t, err)
	assert.Equal(t, query.IntervalHour, i)
	i, err = query.ParseInterval("day")
	require.NoError(t, err)
	assert.Equal(t, query.IntervalDay, i)
	_, err = query.ParseInterval("week")
	require.ErrorIs(t, err, query.ErrInvalidFilter)
}

func TestParseTopBy(t *testing.T) {
	t.Parallel()
	b, err := query.ParseTopBy("")
	require.NoError(t, err)
	assert.Equal(t, query.TopByCost, b)
	b, err = query.ParseTopBy("duration")
	require.NoError(t, err)
	assert.Equal(t, query.TopByDuration, b)
	_, err = query.ParseTopBy("tokens")
	require.ErrorIs(t, err, query.ErrInvalidFilter)
}

func TestOverviewRequest_Validate(t *testing.T) {
	t.Parallel()
	from := time.Unix(0, 0)
	to := from.Add(time.Hour)
	tests := []struct {
		name    string
		r       query.OverviewRequest
		wantErr bool
	}{
		{"valid", query.OverviewRequest{
			From: from, To: to, GroupBy: query.GroupModel,
			Interval: query.IntervalDay, Kind: trace.KindLLM,
		}, false},
		{"defaults", query.OverviewRequest{From: from, To: to}, false},
		{"bad range", query.OverviewRequest{From: to, To: from}, true},
		{"bad group", query.OverviewRequest{From: from, To: to, GroupBy: "x"}, true},
		{"bad interval", query.OverviewRequest{From: from, To: to, Interval: "x"}, true},
		{"bad kind", query.OverviewRequest{From: from, To: to, Kind: "x"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.r.Validate()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTopSpansRequest_Validate(t *testing.T) {
	t.Parallel()
	from := time.Unix(0, 0)
	to := from.Add(time.Hour)
	tests := []struct {
		name    string
		r       query.TopSpansRequest
		wantErr error
	}{
		{"valid", query.TopSpansRequest{From: from, To: to, By: query.TopByCost, Limit: 10}, nil},
		{"defaults", query.TopSpansRequest{From: from, To: to}, nil},
		{"bad range", query.TopSpansRequest{From: to, To: from}, query.ErrInvalidRange},
		{"bad by", query.TopSpansRequest{From: from, To: to, By: "x"}, query.ErrInvalidFilter},
		{"bad limit", query.TopSpansRequest{From: from, To: to, Limit: 501}, query.ErrInvalidLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.r.Validate()
			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}
