// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/cost"
)

// Range bounds the spans a recompute touches by start time: Since inclusive,
// Until exclusive. A zero bound is open.
type Range struct {
	Since, Until time.Time
}

// Store is the write port of the admin use cases. Every method is called only
// from inside a writer job, so it runs on the writer connection and performs at
// most one transaction.
type Store interface {
	// RecomputeCosts reprices, using calc and in one transaction, up to limit
	// spans starting in r whose opaque ascending key (e.g. the span rowid) is
	// greater than afterKey (0 starts from the beginning). It updates span
	// cost_nanos and the trace, session and rollup sums by the delta. It returns
	// the key to pass next time and the number of spans examined; fewer than
	// limit means the range is exhausted.
	RecomputeCosts(
		ctx context.Context, calc *cost.Calculator, r Range, afterKey int64, limit int,
	) (nextKey int64, n int, err error)
	// DeleteUserData deletes up to limit rows (spans, content, trace rows,
	// sessions, per-user rollups) belonging to the user in one transaction and
	// returns the number deleted. It must return a positive n while any data of
	// the user remains and 0 once none is left.
	DeleteUserData(ctx context.Context, userID string, limit int) (n int, err error)
	// IncrementalVacuum frees at most pages free pages in one step and returns the
	// number freed; 0 means the freelist is empty.
	IncrementalVacuum(ctx context.Context, pages int) (freed int, err error)
	// Checkpoint runs wal_checkpoint(TRUNCATE). A busy result is not an error:
	// the WAL is left for the next pass.
	Checkpoint(ctx context.Context) error
}

// Backuper writes a consistent copy of the database.
type Backuper interface {
	// BackupTo runs VACUUM INTO path on a read-only connection. It fails when path
	// already exists.
	BackupTo(ctx context.Context, path string) error
}
