// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/rollup/rolluptest"
)

func durationGen() *rapid.Generator[int64] {
	return rapid.OneOf(
		rapid.Int64Range(-10, rollup.MinValueNs-1),
		rapid.Custom(func(t *rapid.T) int64 {
			shift := rapid.IntRange(10, 46).Draw(t, "octave")
			v := rapid.Int64Range(1<<(shift-1), 1<<shift).Draw(t, "v")
			return min(max(v, rollup.MinValueNs), rollup.MaxValueNs)
		}),
		rapid.Int64Range(rollup.MaxValueNs-1000, rollup.MaxValueNs+1000),
	)
}

func histogramGen() *rapid.Generator[*rollup.Histogram] {
	return rapid.Custom(func(t *rapid.T) *rollup.Histogram {
		return histogramOf(rapid.SliceOfN(durationGen(), 0, 200).Draw(t, "values")...)
	})
}

func blob(t *rapid.T, h *rollup.Histogram) []byte {
	b, err := h.MarshalBinary()
	require.NoError(t, err)
	return b
}

func merged(hs ...*rollup.Histogram) *rollup.Histogram {
	var out rollup.Histogram
	for _, h := range hs {
		out.Merge(h)
	}
	return &out
}

func TestHistogram_Merge_AssociativeProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a, b, c := histogramGen().Draw(t, "a"), histogramGen().Draw(t, "b"), histogramGen().Draw(t, "c")
		left := merged(merged(a, b), c)
		right := merged(a, merged(b, c))
		require.Equal(t, blob(t, left), blob(t, right))
	})
}

func TestHistogram_Merge_CommutativeProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a, b := histogramGen().Draw(t, "a"), histogramGen().Draw(t, "b")
		ab := a.Clone()
		ab.Merge(b)
		ba := b.Clone()
		ba.Merge(a)
		require.Equal(t, blob(t, &ab), blob(t, &ba))
		require.Equal(t, a.Count()+b.Count(), ab.Count())
	})
}

func TestHistogram_Merge_EqualsAddingAllProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		xs := rapid.SliceOf(durationGen()).Draw(t, "xs")
		ys := rapid.SliceOf(durationGen()).Draw(t, "ys")
		got := histogramOf(xs...)
		got.Merge(histogramOf(ys...))
		require.Equal(t, blob(t, histogramOf(slices.Concat(xs, ys)...)), blob(t, got))
	})
}

func TestHistogram_MarshalBinary_RoundTripProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		h := histogramGen().Draw(t, "h")
		b := blob(t, h)
		var got rollup.Histogram
		require.NoError(t, got.UnmarshalBinary(b))
		require.Equal(t, h.Count(), got.Count())
		require.Equal(t, b, blob(t, &got))
		for _, q := range rolluptest.Quantiles {
			want, werr := h.Quantile(q)
			have, herr := got.Quantile(q)
			require.Equal(t, werr, herr)
			require.Equal(t, want, have)
		}
	})
}

func TestHistogram_Quantile_ErrorBoundProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		values := rapid.SliceOfN(rapid.Int64Range(rollup.MinValueNs, rollup.MaxValueNs), 1, 300).Draw(t, "values")
		q := rapid.Float64Range(0, 1).Draw(t, "q")
		h := histogramOf(values...)
		got, err := h.Quantile(q)
		require.NoError(t, err)
		exact := rolluptest.ExactQuantile(rolluptest.Sorted(values), q)
		require.Less(t, rolluptest.RelativeError(got, exact), 1.0/rollup.SubBuckets,
			"q=%v exact=%d got=%d", q, exact, got)
	})
}
