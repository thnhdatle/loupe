// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"slices"
	"sync/atomic"
)

// Stats is a snapshot of the planner's counters, cumulative since New, for
// metrics (loupe_retention_deleted_total{class}, loupe_db_size_bytes).
type Stats struct {
	Passes      int64           // passes completed, with or without errors
	PassErrors  int64           // passes that ended with an error other than shutdown or cancellation
	Deleted     map[Class]int64 // rows deleted by the age rules, every class present
	CapDeleted  map[Class]int64 // rows deleted by the disk cap; keys ClassContent and ClassSpans
	CapExceeded int64           // passes that found the database above max_db_bytes
	// CapUnmet counts passes that ended above max_db_bytes with nothing left
	// to delete but the last hour of data.
	CapUnmet       int64
	CheckpointBusy int64 // wal_checkpoint(TRUNCATE) calls that reported busy
	DBSizeBytes    int64 // last measured size (main + WAL + SHM); 0 until measured
}

type counters struct {
	passes, passErrors, capExceeded, capUnmet, checkpointBusy, dbSize atomic.Int64

	deleted    [6]atomic.Int64 // indexed by classIndex
	capContent atomic.Int64
	capSpans   atomic.Int64
}

// classIndex returns the position of c in Classes; the planner only passes
// the constants.
func classIndex(c Class) int { return slices.Index(Classes(), c) }

func (c *counters) snapshot() Stats {
	s := Stats{
		Passes:         c.passes.Load(),
		PassErrors:     c.passErrors.Load(),
		Deleted:        make(map[Class]int64, len(c.deleted)),
		CapDeleted:     map[Class]int64{ClassContent: c.capContent.Load(), ClassSpans: c.capSpans.Load()},
		CapExceeded:    c.capExceeded.Load(),
		CapUnmet:       c.capUnmet.Load(),
		CheckpointBusy: c.checkpointBusy.Load(),
		DBSizeBytes:    c.dbSize.Load(),
	}
	for _, cl := range Classes() {
		s.Deleted[cl] = c.deleted[classIndex(cl)].Load()
	}
	return s
}
