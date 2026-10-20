// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// entry is one queued item: the data of one ingest request.
type entry struct {
	batch *trace.Batch
	slots int
	bytes int64
	ack   chan error // capacity 1; nil unless the producer waits for the outcome
}

// countKey groups ingest counters by UTC hour (AtNs floored to the hour),
// signal, result and reason, so pending counters stay small however many
// requests add to them.
type countKey struct {
	hour   int64
	signal trace.Signal
	result trace.Result
	reason string
}

// Enqueue adds one item to the queue: b holds the spans, batch-level content
// and counters of one ingest request; it must not be modified afterwards.
// slots is the number of queue slots it takes (its spans plus its log records)
// and bytes its estimated in-memory size. The slots and bytes stay held until
// the batch containing the item commits or is finally dropped.
//
// It returns trace.ErrOverloaded when the item does not fit within the queue
// limits (an item is always admitted into an empty queue, so a single item
// larger than the limits cannot wedge ingest), and trace.ErrShuttingDown once
// shutdown has started. With wait, the returned channel receives exactly one
// value: nil when the item has committed, an error wrapping
// trace.ErrShuttingDown when shutdown abandoned it, or another error when it
// was dropped after the retry budget.
func (w *Writer) Enqueue(b *trace.Batch, slots int, bytes int64, wait bool) (<-chan error, error) {
	if b == nil || slots < 1 || bytes < 0 {
		return nil, fmt.Errorf("%w: need a batch, slots >= 1 and bytes >= 0", ErrInvalidItem)
	}
	e := &entry{batch: b, slots: slots, bytes: bytes}
	if wait {
		e.ack = make(chan error, 1)
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, trace.ErrShuttingDown
	}
	if !w.fits(slots, bytes) {
		w.mu.Unlock()
		return nil, trace.ErrOverloaded
	}
	w.pending = append(w.pending, e)
	w.pendingSlots += slots
	w.usedSlots += slots
	w.usedBytes += bytes
	full := w.pendingSlots >= w.cfg.batchSpans
	w.mu.Unlock()
	if full {
		select {
		case w.wake <- struct{}{}:
		default: // a wake-up is already pending
		}
	}
	return e.ack, nil
}

// HasRoom reports whether an item of slots slots and no bytes would be
// admitted now: the queue is empty, or the slots fit and the byte limit is not
// yet reached. It is the ingest pre-check before translation (ingest.Queue);
// only Enqueue admits.
func (w *Writer) HasRoom(slots int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fits(slots, 0)
}

// fits reports whether an item may be admitted. Callers hold w.mu.
func (w *Writer) fits(slots int, bytes int64) bool {
	if w.usedSlots == 0 && w.usedBytes == 0 {
		return true
	}
	return w.usedSlots+slots <= w.cfg.queueSpans && w.usedBytes+bytes <= w.cfg.queueBytes
}

// AddCount records an ingest counter; it is written with the next batch and
// survives failed batches. Counters with N == 0 are ignored.
func (w *Writer) AddCount(c trace.IngestCount) {
	if c.N == 0 {
		return
	}
	k := countKey{hour: floorHour(c.AtNs), signal: c.Signal, result: c.Result, reason: c.Reason}
	w.mu.Lock()
	w.counts[k] += c.N
	w.mu.Unlock()
}

func floorHour(ns int64) int64 {
	const h = int64(time.Hour)
	return ns - ((ns%h)+h)%h
}

// batchReady reports whether enough slots are queued for a size-triggered flush.
func (w *Writer) batchReady() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pendingSlots >= w.cfg.batchSpans
}

// hasPending reports whether items are queued.
func (w *Writer) hasPending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pending) > 0
}

// hasCounts reports whether ingest counters are pending.
func (w *Writer) hasCounts() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.counts) > 0
}

// take removes the next batch worth of items (whole items, at least one, up to
// batch_spans slots) and every pending counter.
func (w *Writer) take() ([]*entry, []trace.IngestCount) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, slots := 0, 0
	for n < len(w.pending) {
		s := w.pending[n].slots
		if n > 0 && slots+s > w.cfg.batchSpans {
			break
		}
		slots += s
		n++
	}
	entries := slices.Clone(w.pending[:n])
	clear(w.pending[:n])
	if n == len(w.pending) {
		w.pending = w.pending[:0]
	} else {
		w.pending = w.pending[n:]
	}
	w.pendingSlots -= slots
	counts := make([]trace.IngestCount, 0, len(w.counts))
	for k, v := range w.counts {
		counts = append(counts, trace.IngestCount{AtNs: k.hour, Signal: k.signal, Result: k.result, Reason: k.reason, N: v})
	}
	clear(w.counts)
	slices.SortFunc(counts, compareCounts)
	return entries, counts
}

func compareCounts(a, b trace.IngestCount) int {
	return cmp.Or(
		cmp.Compare(a.AtNs, b.AtNs),
		cmp.Compare(a.Signal, b.Signal),
		cmp.Compare(a.Result, b.Result),
		cmp.Compare(a.Reason, b.Reason),
	)
}

// restoreCounts puts counters of a batch that did not commit back into the pending set.
func (w *Writer) restoreCounts(counts []trace.IngestCount) {
	for _, c := range counts {
		w.AddCount(c)
	}
}

// settle releases the slots of entries and delivers outcome to their waiters.
// This is the only place slots are released.
func (w *Writer) settle(entries []*entry, outcome error) {
	slots, bytes := 0, int64(0)
	for _, e := range entries {
		slots += e.slots
		bytes += e.bytes
	}
	w.mu.Lock()
	w.usedSlots -= slots
	w.usedBytes -= bytes
	w.mu.Unlock()
	for _, e := range entries {
		if e.ack != nil {
			e.ack <- outcome
		}
	}
}

// merge builds the store batch for entries and counts. Resources are
// deduplicated by id; spans and content keep queue order.
func merge(entries []*entry, counts []trace.IngestCount) *trace.Batch {
	nSpans, nContent := 0, 0
	for _, e := range entries {
		nSpans += len(e.batch.Spans)
		nContent += len(e.batch.Content)
	}
	b := &trace.Batch{
		Spans:   make([]trace.Span, 0, nSpans),
		Content: make([]trace.ContentPart, 0, nContent),
		Counts:  counts,
	}
	seen := make(map[uint64]struct{})
	for _, e := range entries {
		for _, r := range e.batch.Resources {
			if _, ok := seen[r.ID]; !ok {
				seen[r.ID] = struct{}{}
				b.Resources = append(b.Resources, r)
			}
		}
		b.Spans = append(b.Spans, e.batch.Spans...)
		b.Content = append(b.Content, e.batch.Content...)
		b.Counts = append(b.Counts, e.batch.Counts...)
	}
	return b
}

// spanAndLogSlots splits the slots of entries into spans and log records.
func spanAndLogSlots(entries []*entry) (spans, logs int64) {
	for _, e := range entries {
		spans += int64(len(e.batch.Spans))
		logs += int64(max(0, e.slots-len(e.batch.Spans)))
	}
	return spans, logs
}
