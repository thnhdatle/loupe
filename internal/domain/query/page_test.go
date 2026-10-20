// SPDX-License-Identifier: Apache-2.0

package query_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
)

func TestNormalizeLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      int
		want    int
		wantErr bool
	}{
		{"default", 0, 50, false},
		{"one", 1, 1, false},
		{"max", 500, 500, false},
		{"above max", 501, 0, true},
		{"negative", -1, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := query.NormalizeLimit(tc.in)
			if tc.wantErr {
				require.ErrorIs(t, err, query.ErrInvalidLimit)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseSortOrder(t *testing.T) {
	t.Parallel()
	s, err := query.ParseSort("")
	require.NoError(t, err)
	assert.Equal(t, query.SortStart, s)
	for _, in := range []string{"start", "cost", "duration"} {
		s, err = query.ParseSort(in)
		require.NoError(t, err)
		assert.Equal(t, query.Sort(in), s)
	}
	_, err = query.ParseSort("bogus")
	require.ErrorIs(t, err, query.ErrInvalidSort)

	o, err := query.ParseOrder("")
	require.NoError(t, err)
	assert.Equal(t, query.OrderDesc, o)
	o, err = query.ParseOrder("asc")
	require.NoError(t, err)
	assert.Equal(t, query.OrderAsc, o)
	_, err = query.ParseOrder("up")
	require.ErrorIs(t, err, query.ErrInvalidSort)
}

func TestResolveRange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 19, 12, 30, 0, 0, time.UTC)
	cet := time.FixedZone("x", 3600)
	from := time.Date(2026, 10, 19, 9, 0, 0, 0, cet)
	to := time.Date(2026, 10, 19, 11, 0, 0, 0, cet)
	tests := []struct {
		name         string
		from, to     *time.Time
		wantF, wantT time.Time
		wantErr      bool
	}{
		{"defaults", nil, nil, now.Add(-24 * time.Hour), now, false},
		{"explicit utc", &from, &to, from.UTC(), to.UTC(), false},
		{"only from", &from, nil, from.UTC(), now, false},
		{"only to", nil, &to, to.UTC().Add(-24 * time.Hour), to.UTC(), false},
		{"equal", &to, &to, time.Time{}, time.Time{}, true},
		{"reversed", &to, &from, time.Time{}, time.Time{}, true},
		{"from after now", &to, nil, time.Time{}, time.Time{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gf, gt, err := query.ResolveRange(tc.from, tc.to, now)
			if tc.wantErr {
				require.ErrorIs(t, err, query.ErrInvalidRange)
				return
			}
			require.NoError(t, err)
			if !tc.wantF.IsZero() {
				assert.True(t, tc.wantF.Equal(gf))
				assert.True(t, tc.wantT.Equal(gt))
			}
			assert.Equal(t, time.UTC, gf.Location())
			assert.Equal(t, time.UTC, gt.Location())
		})
	}
}

func TestRoundOutwardHours(t *testing.T) {
	t.Parallel()
	d := func(h, m int) time.Time { return time.Date(2026, 10, 19, h, m, 0, 0, time.UTC) }
	tests := []struct {
		name         string
		from, to     time.Time
		wantF, wantT time.Time
	}{
		{"inside hours", d(9, 20), d(11, 5), d(9, 0), d(12, 0)},
		{"already whole", d(9, 0), d(11, 0), d(9, 0), d(11, 0)},
		{"to sub-second", d(9, 0), d(11, 0).Add(time.Nanosecond), d(9, 0), d(12, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, to := query.RoundOutwardHours(tc.from, tc.to)
			assert.Equal(t, tc.wantF, f)
			assert.Equal(t, tc.wantT, to)
		})
	}
}
