// SPDX-License-Identifier: Apache-2.0

package metrics

// Histogram buckets, declared once per project.
var (
	// batchDurationBuckets are seconds for one writer transaction.
	batchDurationBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	// batchSpanBuckets are the spans of one writer batch (batch_spans defaults to 2000).
	batchSpanBuckets = []float64{1, 10, 50, 100, 250, 500, 1000, 2000, 5000, 10000}
	// queryDurationBuckets are seconds for one API or dashboard query (the target is p95 < 1 s).
	queryDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
)
