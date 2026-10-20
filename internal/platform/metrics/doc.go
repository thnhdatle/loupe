// SPDX-License-Identifier: Apache-2.0

// Package metrics exposes Loupe's Prometheus metrics.
//
// Counters that the writer and the retention planner already keep are read
// from their Stats snapshots at scrape time; everything else is recorded
// through the Observe*, Add* and Count methods. All label values come from
// fixed sets or are capped (models, endpoints), so cardinality stays bounded.
// Metrics owns its registry; there is no global state.
//
// Usage:
//
//	m, err := metrics.New(metrics.Sources{Writer: w.Stats, Retention: p.Stats}, metrics.BuildInfo{Version: v})
//	mux.Handle("/metrics", m.Handler())
//	q := metrics.WrapQueue(w, m) // counts every ingest.Queue.AddCount
package metrics
