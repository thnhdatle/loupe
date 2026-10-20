// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/rollup/rolluptest"
)

// unitSamples is the per-distribution sample size of the unit test; the acceptance check uses 1M.
const unitSamples = 100_000

// TestHistogram_Quantile_ReferenceDistributions spreads each distribution over 24
// hourly histograms, round-trips them through the blob encoding, merges them and
// compares p50/p95/p99 with the nearest-rank exact quantile.
func TestHistogram_Quantile_ReferenceDistributions(t *testing.T) {
	t.Parallel()
	for i, d := range rolluptest.Distributions() {
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()
			values := rolluptest.Samples(d, i, unitSamples)
			var hours [24]rollup.Histogram
			for j, v := range values {
				hours[j%len(hours)].Add(v)
			}
			var total rollup.Histogram
			for h := range hours {
				b, err := hours[h].MarshalBinary()
				require.NoError(t, err)
				var decoded rollup.Histogram
				require.NoError(t, decoded.UnmarshalBinary(b))
				total.Merge(&decoded)
			}
			require.Equal(t, uint64(unitSamples), total.Count())
			sorted := rolluptest.Sorted(values)
			for _, q := range rolluptest.Quantiles {
				got, err := total.Quantile(q)
				require.NoError(t, err)
				exact := rolluptest.ExactQuantile(sorted, q)
				relErr := rolluptest.RelativeError(got, exact)
				t.Logf("%s p%v: exact=%d estimate=%d error=%.4f%%", d.Name, q*100, exact, got, relErr*100)
				assert.LessOrEqual(t, relErr, 0.02, "seed %d, q=%v", rolluptest.Seed, q)
			}
		})
	}
}
