// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"sync/atomic"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Stats is a snapshot of the writer's queue and counters, for metrics and
// readiness. Counters are cumulative since New.
type Stats struct {
	QueuedSlots int   // slots held by queued items and the batch being written
	QueuedBytes int64 // estimated bytes held by the same items
	QueuedJobs  int   // submitted jobs waiting for the writer
	Batches     int64 // committed batches, including counter-only ones
	Committed   int64 // slots (spans and log records) in committed batches
	Inserted    int64 // spans the store reported as inserted
	Duplicates  int64 // spans the store ignored as already stored
	Retries     int64 // WriteBatch attempts after the first, over all batches
	// DroppedStoreError counts slots dropped after the retry budget (store_error).
	DroppedStoreError int64
	// DroppedShutdown counts slots abandoned at the shutdown deadline (shutdown).
	DroppedShutdown int64
	Jobs            int64 // jobs run
	JobErrors       int64 // jobs that returned an error or panicked
}

type counters struct {
	batches, committed, inserted, duplicates, retries atomic.Int64
	droppedStoreError, droppedShutdown                atomic.Int64
	jobs, jobErrors                                   atomic.Int64
}

func (c *counters) recordCommit(entries []*entry, res trace.BatchResult) {
	slots := 0
	for _, e := range entries {
		slots += e.slots
	}
	c.batches.Add(1)
	c.committed.Add(int64(slots))
	c.inserted.Add(int64(res.Inserted))
	c.duplicates.Add(int64(res.Duplicates))
}

// Stats returns a snapshot of the queue and counters.
func (w *Writer) Stats() Stats {
	w.mu.Lock()
	slots, bytes := w.usedSlots, w.usedBytes
	w.mu.Unlock()
	return Stats{
		QueuedSlots:       slots,
		QueuedBytes:       bytes,
		QueuedJobs:        len(w.jobs),
		Batches:           w.stats.batches.Load(),
		Committed:         w.stats.committed.Load(),
		Inserted:          w.stats.inserted.Load(),
		Duplicates:        w.stats.duplicates.Load(),
		Retries:           w.stats.retries.Load(),
		DroppedStoreError: w.stats.droppedStoreError.Load(),
		DroppedShutdown:   w.stats.droppedShutdown.Load(),
		Jobs:              w.stats.jobs.Load(),
		JobErrors:         w.stats.jobErrors.Load(),
	}
}
