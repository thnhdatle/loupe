// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"context"
	"time"
)

// Class is a retention class: a set of rows deleted by one rule. Its value is
// the class label of loupe_retention_deleted_total.
type Class string

// Retention classes, in the order a pass deletes them.
const (
	// ClassContent: span_content rows whose time (time_ns: the span start for
	// span parts, the event time for log-event parts) is before the cutoff.
	ClassContent Class = "content"
	// ClassOrphanContent: span_content rows before the cutoff whose
	// (trace_id, span_id) has no stored span.
	ClassOrphanContent Class = "orphan_content"
	// ClassSpans: spans whose start_ns is before the cutoff. In the same
	// transaction the store deletes the content rows of the deleted spans and
	// the trace rows left without spans. Totals of surviving trace and session
	// rows are not adjusted: a trace straddling the cutoff keeps its totals
	// until its last span goes. Content and trace rows do not count towards
	// the limit or the returned count.
	ClassSpans Class = "spans"
	// ClassSessions: session rows that no stored trace references and whose
	// last activity is before the cutoff. The planner passes now as cutoff.
	ClassSessions Class = "sessions"
	// ClassRollupsHourly: rollup_hour and rollup_user_hour rows whose whole
	// bucket lies before the cutoff (bucket start + 1h ≤ cutoff).
	ClassRollupsHourly Class = "rollups_hourly"
	// ClassRollupsDaily: rollup_day and rollup_user_day rows whose whole UTC
	// day lies before the cutoff (day start + 24h ≤ cutoff), and ingest_stats
	// rows whose whole hour lies before the cutoff.
	ClassRollupsDaily Class = "rollups_daily"
)

// Classes lists every class in pass order.
func Classes() []Class {
	return []Class{
		ClassContent, ClassOrphanContent, ClassSpans, ClassSessions,
		ClassRollupsHourly, ClassRollupsDaily,
	}
}

// Store is the storage port of the planner, implemented by the SQLite adapter.
// Purge, IncrementalVacuum and Checkpoint write, so the planner calls them only
// inside trace.Job functions, which run on the single writer goroutine; Size
// only reads file sizes and is called from the planner's goroutine.
type Store interface {
	// Purge deletes, in one transaction, at most limit rows of class c that are
	// older than cutoff (the age rule of each class is on its constant),
	// oldest first, and returns the number of rows of c deleted. When the
	// class spans several tables, limit and the count cover them all. A count
	// below limit means no qualifying row was left when the transaction ran.
	// limit is at least 1. On error nothing is deleted.
	Purge(ctx context.Context, c Class, cutoff time.Time, limit int) (int, error)
	// Size returns the bytes on disk of the database: main file + WAL + SHM
	// (a missing WAL or SHM counts 0). It must not write.
	Size(ctx context.Context) (int64, error)
	// IncrementalVacuum runs PRAGMA incremental_vacuum(maxPages), releasing at
	// most maxPages free pages to the file system, and returns the free pages
	// still left (PRAGMA freelist_count). maxPages is at least 1.
	IncrementalVacuum(ctx context.Context, maxPages int) (remaining int, err error)
	// Checkpoint runs PRAGMA wal_checkpoint(TRUNCATE). busy is true when it
	// could not complete because a reader held the WAL past busy_timeout; that
	// is not an error, and the WAL is left for the next pass.
	Checkpoint(ctx context.Context) (busy bool, err error)
}
