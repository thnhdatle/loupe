// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
)

func histogramOf(values ...int64) *rollup.Histogram {
	var h rollup.Histogram
	for _, v := range values {
		h.Add(v)
	}
	return &h
}

func encoded(t *testing.T, h *rollup.Histogram) []byte {
	t.Helper()
	b, err := h.MarshalBinary()
	require.NoError(t, err)
	return b
}

func TestHistogram_Quantile_Empty(t *testing.T) {
	t.Parallel()
	var h rollup.Histogram
	assert.True(t, h.IsEmpty())
	assert.Zero(t, h.Count())
	_, err := h.Quantile(0.5)
	require.ErrorIs(t, err, rollup.ErrEmptyHistogram)
}

func TestHistogram_Quantile_InvalidQ(t *testing.T) {
	t.Parallel()
	h := histogramOf(int64(time.Millisecond))
	for _, q := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		_, err := h.Quantile(q)
		require.ErrorIs(t, err, rollup.ErrInvalidQuantile, "q=%v", q)
	}
}

func TestHistogram_Quantile_SingleValue(t *testing.T) {
	t.Parallel()
	v := int64(1234567)
	h := histogramOf(v)
	for _, q := range []float64{0, 0.5, 0.99, 1} {
		got, err := h.Quantile(q)
		require.NoError(t, err)
		assert.InEpsilon(t, float64(v), float64(got), 1.0/rollup.SubBuckets, "q=%v", q)
	}
}

func TestHistogram_Quantile_ZeroAndOneAreExtremes(t *testing.T) {
	t.Parallel()
	h := histogramOf(5_000, 1_000_000, 2_000_000_000)
	lo, err := h.Quantile(0)
	require.NoError(t, err)
	hi, err := h.Quantile(1)
	require.NoError(t, err)
	assert.InEpsilon(t, 5_000.0, float64(lo), 0.01)
	assert.InEpsilon(t, 2e9, float64(hi), 0.01)
}

func TestHistogram_Quantile_UnderflowAndOverflow(t *testing.T) {
	t.Parallel()
	h := histogramOf(-10, 0, 999, rollup.MaxValueNs+1, int64(48*time.Hour))
	assert.Equal(t, uint64(5), h.Count())
	cases := []struct {
		name string
		q    float64
		want int64
	}{
		{name: "rank 1 of 3 underflow", q: 0.2, want: 166},
		{name: "rank 3 of 3 underflow", q: 0.6, want: 833},
		{name: "overflow", q: 0.8, want: rollup.MaxValueNs},
		{name: "max overflow", q: 1, want: rollup.MaxValueNs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := h.Quantile(tc.q)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestHistogram_Quantile_MaxValueStaysInRange(t *testing.T) {
	t.Parallel()
	h := histogramOf(rollup.MaxValueNs, rollup.MaxValueNs)
	got, err := h.Quantile(1)
	require.NoError(t, err)
	assert.LessOrEqual(t, got, rollup.MaxValueNs)
	assert.InEpsilon(t, float64(rollup.MaxValueNs), float64(got), 0.01)
}

func TestHistogram_Quantile_InterpolatesByRank(t *testing.T) {
	t.Parallel()
	// 1000..1007 share the first bucket [1000, 1008): ranks map to slot centres.
	h := histogramOf(1000, 1001, 1002, 1003, 1004, 1005, 1006, 1007)
	for r := 1; r <= 8; r++ {
		got, err := h.Quantile(float64(r) / 8)
		require.NoError(t, err)
		assert.Equal(t, int64(1000+r-1), got, "rank %d", r)
	}
}

func TestNearestRank(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		q    float64
		n    uint64
		want uint64
	}{
		{name: "empty", q: 0.5, n: 0, want: 0},
		{name: "q zero clamps to 1", q: 0, n: 10, want: 1},
		{name: "exact", q: 0.5, n: 10, want: 5},
		{name: "ceil", q: 0.51, n: 10, want: 6},
		{name: "q one", q: 1, n: 10, want: 10},
		{name: "p99 of 1M", q: 0.99, n: 1_000_000, want: 990_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, rollup.NearestRank(tc.q, tc.n))
		})
	}
}

func TestHistogram_Add_OutOfOrder(t *testing.T) {
	t.Parallel()
	a := histogramOf(5e9, 1e3, 3e6, 1e3, 7e7, 3e6)
	b := histogramOf(1e3, 1e3, 3e6, 3e6, 7e7, 5e9)
	assert.Equal(t, encoded(t, b), encoded(t, a))
	assert.Equal(t, uint64(6), a.Count())
}

func TestHistogram_Merge_Cases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b []int64
	}{
		{name: "both empty"},
		{name: "into empty", b: []int64{1e3, 2e6}},
		{name: "from empty", a: []int64{1e3, 2e6}},
		{name: "same buckets", a: []int64{1e3, 2e6}, b: []int64{1e3, 2e6, 2e6}},
		{name: "disjoint after", a: []int64{1e3}, b: []int64{2e6, 3e9}},
		{name: "disjoint before", a: []int64{3e9}, b: []int64{1e3, 2e6}},
		{name: "interleaved", a: []int64{1e3, 5e6, 9e9}, b: []int64{2e4, 5e6, 7e8, 1e11}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := histogramOf(tc.a...)
			got.Merge(histogramOf(tc.b...))
			want := histogramOf(append(append([]int64{}, tc.a...), tc.b...)...)
			assert.Equal(t, encoded(t, want), encoded(t, got))
			assert.Equal(t, want.Count(), got.Count())
		})
	}
}

func TestHistogram_Merge_NilAndSelf(t *testing.T) {
	t.Parallel()
	h := histogramOf(1e3, 2e6)
	h.Merge(nil)
	assert.Equal(t, uint64(2), h.Count())
	h.Merge(h)
	assert.Equal(t, encoded(t, histogramOf(1e3, 1e3, 2e6, 2e6)), encoded(t, h))
}

func TestHistogram_Merge_DoesNotRetainSource(t *testing.T) {
	t.Parallel()
	src := histogramOf(1e3, 2e6)
	var dst rollup.Histogram
	dst.Merge(src)
	src.Add(1e3)
	src.Add(9e9)
	assert.Equal(t, encoded(t, histogramOf(1e3, 2e6)), encoded(t, &dst))
}

func TestHistogram_CloneAndReset(t *testing.T) {
	t.Parallel()
	h := histogramOf(1e3, 2e6)
	c := h.Clone()
	h.Add(3e9)
	assert.Equal(t, uint64(2), c.Count())
	h.Reset()
	assert.True(t, h.IsEmpty())
	assert.Equal(t, encoded(t, &rollup.Histogram{}), encoded(t, h))
	var empty rollup.Histogram
	ec := empty.Clone()
	assert.True(t, ec.IsEmpty())
}
