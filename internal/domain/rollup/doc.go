// SPDX-License-Identifier: Apache-2.0

// Package rollup defines the hourly and daily rollup rows: keys, additive
// measures, the log-linear latency histogram with its blob encoding,
// range assembly from day and edge-hour rows, the group_by=user top-N split and
// the RollupReader port.
//
// Usage:
//
//	var a rollup.Aggregate
//	a.AddSpan(&span)
//	p95, err := a.Histogram.Quantile(0.95)
//	blob, _ := a.Histogram.MarshalBinary()
package rollup
