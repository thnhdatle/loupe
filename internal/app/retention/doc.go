// SPDX-License-Identifier: Apache-2.0

// Package retention plans retention, disk-cap and orphan-content jobs and
// submits them to the writer as jobs, then checkpoints the WAL.
//
// Every retention.interval the Planner deletes, class by class, the rows past
// their age: content, orphan content, spans, sessions without traces, hourly
// and daily rollups (with user rollups and ingest_stats). Each deletion is one
// trace.Job of at most retention.batch rows, submitted until a job deletes
// fewer, so ingest batches run in between. Then it runs incremental-vacuum jobs
// and one wal_checkpoint(TRUNCATE) job. With retention.max_db_bytes set it
// then deletes the oldest content, then the oldest spans, batch by batch,
// compacting and re-measuring after each, but never the last hour (CapFloor).
//
// Main types: Planner, Config, Store (the storage port), Class and Stats.
//
// Usage:
//
//	p, err := retention.New(store, w, clock.System{}, cfg, retention.WithLogger(log))
//	if err != nil {
//		return err
//	}
//	g.Go(func() error { return p.Run(ctx) })
package retention
