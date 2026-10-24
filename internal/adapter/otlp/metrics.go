// SPDX-License-Identifier: Apache-2.0

package otlp

import metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

func countDataPoints(rms []*metricspb.ResourceMetrics) int64 {
	var n int64
	for _, rm := range rms {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				n += metricPoints(m)
			}
		}
	}
	return n
}

func metricPoints(m *metricspb.Metric) int64 {
	switch d := m.GetData().(type) {
	case *metricspb.Metric_Gauge:
		return int64(len(d.Gauge.GetDataPoints()))
	case *metricspb.Metric_Sum:
		return int64(len(d.Sum.GetDataPoints()))
	case *metricspb.Metric_Histogram:
		return int64(len(d.Histogram.GetDataPoints()))
	case *metricspb.Metric_ExponentialHistogram:
		return int64(len(d.ExponentialHistogram.GetDataPoints()))
	case *metricspb.Metric_Summary:
		return int64(len(d.Summary.GetDataPoints()))
	default:
		return 0
	}
}
