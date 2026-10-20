// SPDX-License-Identifier: Apache-2.0

// Package rolluptest holds the reference latency distributions of the percentile accuracy check and the
// nearest-rank reference, shared by the rollup unit tests and the acceptance test.
//
// Usage:
//
//	for i, d := range rolluptest.Distributions() {
//		values := rolluptest.Samples(d, i, 100_000)
//		exact := rolluptest.ExactQuantile(rolluptest.Sorted(values), 0.99)
//	}
package rolluptest
