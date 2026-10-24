// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"sync/atomic"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type Stats struct {
	QueuedSlots       int
	QueuedBytes       int64
	QueuedJobs        int
	Batches           int64
	Committed         int64
	Inserted          int64
	Duplicates        int64
	Retries           int64
	DroppedStoreError int64
	DroppedShutdown   int64
	Jobs              int64
	JobErrors         int64
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
