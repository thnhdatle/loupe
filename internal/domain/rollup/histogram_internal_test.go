// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBucketIndex_Boundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		v    int64
		want int
	}{
		{name: "negative", v: -5, want: underflowIndex},
		{name: "zero", v: 0, want: underflowIndex},
		{name: "below min", v: MinValueNs - 1, want: underflowIndex},
		{name: "min", v: MinValueNs, want: 1},
		{name: "end of first bucket", v: 1007, want: 1},
		{name: "second bucket", v: 1008, want: 2},
		{name: "last of octave 9", v: 1023, want: 3},
		{name: "first of octave 10", v: 1024, want: 4},
		{name: "max", v: MaxValueNs, want: lastRegular},
		{name: "above max", v: MaxValueNs + 1, want: overflowIndex},
		{name: "max int64", v: 1<<63 - 1, want: overflowIndex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, bucketIndex(tc.v))
		})
	}
	assert.Equal(t, NumBuckets-1, overflowIndex)
}

func TestBucketBounds_ContiguousAndNarrow(t *testing.T) {
	t.Parallel()
	uLo, uHi := bucketBounds(underflowIndex)
	assert.Equal(t, [2]int64{0, MinValueNs}, [2]int64{uLo, uHi})
	prevHi := uHi
	for idx := 1; idx <= lastRegular; idx++ {
		lo, hi := bucketBounds(idx)
		require.Equal(t, prevHi, lo, "bucket %d starts where %d ends", idx, idx-1)
		require.Less(t, lo, hi)
		// Relative width ≤ 1/64 bounds the in-bucket error.
		require.LessOrEqual(t, float64(hi-lo)/float64(lo), 1.0/SubBuckets, "bucket %d", idx)
		require.Equal(t, idx, bucketIndex(lo))
		require.Equal(t, idx, bucketIndex(hi-1))
		prevHi = hi
	}
	assert.Equal(t, MaxValueNs+1, prevHi)
	oLo, oHi := bucketBounds(overflowIndex)
	assert.Equal(t, [2]int64{MaxValueNs + 1, MaxValueNs + 1}, [2]int64{oLo, oHi})
}

func TestEstimate_SlotCentres(t *testing.T) {
	t.Parallel()
	lo, hi := bucketBounds(1) // [1000, 1008)
	require.Equal(t, [2]int64{1000, 1008}, [2]int64{lo, hi})
	assert.Equal(t, int64(1004), estimate(1, 1, 1))
	assert.Equal(t, int64(1001), estimate(1, 1, 4))
	assert.Equal(t, int64(1007), estimate(1, 4, 4))
	assert.Equal(t, int64(500), estimate(underflowIndex, 1, 1))
	assert.Equal(t, MaxValueNs, estimate(overflowIndex, 1, 1))
}

func TestHistogram_Quantile_InconsistentTotal(t *testing.T) {
	t.Parallel()
	// Unreachable through the API; guards the fallback after the bucket walk.
	h := Histogram{buckets: []bucket{{idx: 1, n: 1}}, total: 3}
	got, err := h.Quantile(1)
	require.NoError(t, err)
	assert.Equal(t, MaxValueNs, got)
}
