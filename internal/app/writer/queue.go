// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type entry struct {
	batch *trace.Batch
	slots int
	bytes int64
	ack   chan error
}

type countKey struct {
	hour   int64
	signal trace.Signal
	result trace.Result
	reason string
}

// Enqueue adds the items of one ingest request to the queue.
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
		default:
		}
	}
	return e.ack, nil
}

func (w *Writer) HasRoom(slots int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fits(slots, 0)
}

func (w *Writer) fits(slots int, bytes int64) bool {
	if w.usedSlots == 0 && w.usedBytes == 0 {
		return true
	}
	return w.usedSlots+slots <= w.cfg.queueSpans && w.usedBytes+bytes <= w.cfg.queueBytes
}

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

func (w *Writer) batchReady() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pendingSlots >= w.cfg.batchSpans
}

func (w *Writer) hasPending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pending) > 0
}

func (w *Writer) hasCounts() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.counts) > 0
}

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

func (w *Writer) restoreCounts(counts []trace.IngestCount) {
	for _, c := range counts {
		w.AddCount(c)
	}
}

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

func spanAndLogSlots(entries []*entry) (spans, logs int64) {
	for _, e := range entries {
		spans += int64(len(e.batch.Spans))
		logs += int64(max(0, e.slots-len(e.batch.Spans)))
	}
	return spans, logs
}
