// SPDX-License-Identifier: Apache-2.0

// Package writer is the single store writer: it owns
// the bounded ingest queue, groups queued items into batches of
// writer.batch_spans or every writer.batch_interval, writes each batch in one
// SpanStore.WriteBatch call with bounded jittered retries, releases queue slots
// only when a batch commits or is finally dropped, wakes sync_ack waiters, and
// runs trace.Job work (retention, admin) serially between batches.
//
// Main types: Writer (implements the queue used by app/ingest and
// trace.JobRunner) and Stats.
//
// Usage:
//
//	w, err := writer.New(store, clock.System{}, writer.WithBatchSpans(2000))
//	if err != nil {
//		return err
//	}
//	g.Go(func() error { return w.Run(ctx) })
//	err = w.Submit(ctx, trace.Job{Name: "retention_spans", Run: deleteOldSpans})
package writer
