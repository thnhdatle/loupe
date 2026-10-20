// SPDX-License-Identifier: Apache-2.0

// Package queries holds the use cases behind the REST API and dashboard.
//
// Service validates requests, applies the default time range from the clock and
// delegates to the read ports of domain/query and domain/rollup. Statistics are
// assembled from day rollup rows for whole UTC days and hour rows for the edge
// hours (or for the whole range when the interval is hour).
//
// Usage:
//
//	svc := queries.New(queries.Readers{Traces: tr, Sessions: se, Rollups: ro, Stats: st}, clock.System{})
//	page, err := svc.ListTraces(ctx, queries.TraceListRequest{Page: query.Page{Limit: 50}})
//	ov, err := svc.Overview(ctx, query.OverviewRequest{GroupBy: query.GroupModel, Interval: query.IntervalDay})
package queries
