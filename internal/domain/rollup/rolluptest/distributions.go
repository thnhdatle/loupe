// SPDX-License-Identifier: Apache-2.0

package rolluptest

import (
	"math"
	"math/rand/v2"
	"slices"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
)

// Seed is the fixed seed of the reference samples.
const Seed uint64 = 20261019

// Quantiles are the quantiles the accuracy check compares.
var Quantiles = [...]float64{0.50, 0.95, 0.99}

// Distribution is one reference latency distribution.
type Distribution struct {
	Name   string
	Sample func(r *rand.Rand) int64 // one duration in nanoseconds
}

const (
	ms = 1e6
	s  = 1e9
)

// Distributions returns the five reference distributions in a fixed order.
func Distributions() []Distribution {
	return []Distribution{
		{Name: "lognormal", Sample: func(r *rand.Rand) int64 { return lognormal(r, 500*ms, 1) }},
		{Name: "bimodal", Sample: func(r *rand.Rand) int64 {
			if r.Float64() < 0.7 {
				return lognormal(r, 200*ms, 0.3)
			}
			return lognormal(r, 3*s, 0.3)
		}},
		{Name: "uniform", Sample: func(r *rand.Rand) int64 { return int64(1*ms + r.Float64()*(10*s-1*ms)) }},
		{Name: "exponential", Sample: func(r *rand.Rand) int64 { return int64(r.ExpFloat64() * 1 * s) }},
		{Name: "pareto", Sample: func(r *rand.Rand) int64 {
			// Inverse CDF; 1 − U lies in (0, 1], so the sample is finite.
			return int64(100 * ms / math.Pow(1-r.Float64(), 1/1.5))
		}},
	}
}

func lognormal(r *rand.Rand, median, sigma float64) int64 {
	return int64(median * math.Exp(sigma*r.NormFloat64()))
}

// NewRand returns the deterministic generator for distribution number i.
func NewRand(i int) *rand.Rand {
	return rand.New(rand.NewPCG(Seed, uint64(i))) //nolint:gosec // G404: fixed seeds for a reproducible test.
}

// Samples draws n values of d with the generator of distribution number i.
func Samples(d Distribution, i, n int) []int64 {
	r := NewRand(i)
	out := make([]int64, n)
	for j := range out {
		out[j] = d.Sample(r)
	}
	return out
}

// ExactQuantile returns the nearest-rank q-quantile of sorted values
// (rollup.NearestRank). It panics on an empty slice.
func ExactQuantile(sorted []int64, q float64) int64 {
	return sorted[rollup.NearestRank(q, uint64(len(sorted)))-1]
}

// Sorted returns a sorted copy of values.
func Sorted(values []int64) []int64 {
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}

// RelativeError returns |estimate − exact| / exact; 0 when both are 0.
func RelativeError(estimate, exact int64) float64 {
	if exact == 0 {
		if estimate == 0 {
			return 0
		}
		return math.Inf(1)
	}
	return math.Abs(float64(estimate-exact)) / float64(exact)
}
