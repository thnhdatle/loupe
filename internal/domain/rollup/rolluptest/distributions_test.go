// SPDX-License-Identifier: Apache-2.0

package rolluptest_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/rollup/rolluptest"
)

func TestDistributions_Samples_MediansMatchDefinition(t *testing.T) {
	t.Parallel()
	want := map[string]float64{
		"lognormal":   500e6,
		"bimodal":     200e6 * math.Exp(0.3*0.5659),
		"uniform":     5.0005e9,
		"exponential": math.Ln2 * 1e9,
		"pareto":      100e6 * math.Pow(2, 1/1.5),
	}
	ds := rolluptest.Distributions()
	require.Len(t, ds, 5)
	for i, d := range ds {
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()
			sorted := rolluptest.Sorted(rolluptest.Samples(d, i, 200_000))
			got := float64(rolluptest.ExactQuantile(sorted, 0.5))
			assert.InEpsilon(t, want[d.Name], got, 0.02)
		})
	}
}

func TestSamples_SameSeed_Deterministic(t *testing.T) {
	t.Parallel()
	d := rolluptest.Distributions()[0]
	first, second := rolluptest.Samples(d, 0, 100), rolluptest.Samples(d, 0, 100)
	assert.Equal(t, first, second)
	assert.NotEqual(t, rolluptest.Samples(d, 0, 100), rolluptest.Samples(d, 1, 100))
}

func TestExactQuantile_NearestRank(t *testing.T) {
	t.Parallel()
	sorted := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	cases := []struct {
		name string
		q    float64
		want int64
	}{
		{name: "zero", q: 0, want: 10},
		{name: "p50", q: 0.5, want: 50},
		{name: "p55", q: 0.55, want: 60},
		{name: "p95", q: 0.95, want: 100},
		{name: "one", q: 1, want: 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, rolluptest.ExactQuantile(sorted, tc.q))
		})
	}
}

func TestSorted_DoesNotModifyInput(t *testing.T) {
	t.Parallel()
	in := []int64{3, 1, 2}
	assert.Equal(t, []int64{1, 2, 3}, rolluptest.Sorted(in))
	assert.Equal(t, []int64{3, 1, 2}, in)
}

func TestRelativeError(t *testing.T) {
	t.Parallel()
	assert.InDelta(t, 0.1, rolluptest.RelativeError(110, 100), 1e-12)
	assert.InDelta(t, 0.1, rolluptest.RelativeError(90, 100), 1e-12)
	assert.Zero(t, rolluptest.RelativeError(0, 0))
	assert.True(t, math.IsInf(rolluptest.RelativeError(1, 0), 1))
}
