// SPDX-License-Identifier: Apache-2.0

package fakespanstore

import (
	"context"
	"slices"
	"sync"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type spanKey struct {
	trace trace.ID
	span  trace.SpanID
}

// Store is an in-memory trace.SpanStore with injectable errors.
type Store struct {
	mu        sync.Mutex
	calls     int
	committed []trace.Batch
	stored    map[spanKey]trace.Span
	failNext  []error
	failAll   error
	gate      chan struct{}
	onWrite   func(b *trace.Batch)
}

var _ trace.SpanStore = (*Store)(nil)

// New returns an empty Store.
func New() *Store { return &Store{stored: make(map[spanKey]trace.Span)} }

func (s *Store) FailNext(errs ...error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = append(s.failNext, errs...)
}

func (s *Store) FailAlways(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAll = err
}

func (s *Store) Hold() (release func()) {
	gate := make(chan struct{})
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.gate == gate {
				s.gate = nil
			}
			s.mu.Unlock()
			close(gate)
		})
	}
}

func (s *Store) OnWrite(fn func(b *trace.Batch)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onWrite = fn
}

func (s *Store) WriteBatch(ctx context.Context, b *trace.Batch) (trace.BatchResult, error) {
	s.mu.Lock()
	s.calls++
	gate, onWrite := s.gate, s.onWrite
	s.mu.Unlock()
	if onWrite != nil {
		onWrite(b)
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return trace.BatchResult{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return trace.BatchResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failNext) > 0 {
		err := s.failNext[0]
		s.failNext = s.failNext[1:]
		return trace.BatchResult{}, err
	}
	if s.failAll != nil {
		return trace.BatchResult{}, s.failAll
	}
	var res trace.BatchResult
	for i := range b.Spans {
		k := spanKey{trace: b.Spans[i].TraceID, span: b.Spans[i].SpanID}
		if _, dup := s.stored[k]; dup {
			res.Duplicates++
			continue
		}
		s.stored[k] = b.Spans[i]
		res.Inserted++
	}
	s.committed = append(s.committed, cloneBatch(b))
	return res, nil
}

func (s *Store) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *Store) Batches() []trace.Batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]trace.Batch, len(s.committed))
	for i := range s.committed {
		out[i] = cloneBatch(&s.committed[i])
	}
	return out
}

func (s *Store) SpanCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.stored)
}

func (s *Store) Span(tid trace.ID, sid trace.SpanID) (trace.Span, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.stored[spanKey{trace: tid, span: sid}]
	return sp, ok
}

func (s *Store) Content() []trace.ContentPart {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []trace.ContentPart
	for i := range s.committed {
		out = append(out, s.committed[i].Content...)
	}
	return out
}

func (s *Store) Counts() map[CountKey]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[CountKey]int64)
	for i := range s.committed {
		for _, c := range s.committed[i].Counts {
			out[CountKey{Signal: c.Signal, Result: c.Result, Reason: c.Reason}] += c.N
		}
	}
	return out
}

type CountKey struct {
	Signal trace.Signal
	Result trace.Result
	Reason string
}

func cloneBatch(b *trace.Batch) trace.Batch {
	return trace.Batch{
		Resources: slices.Clone(b.Resources),
		Spans:     slices.Clone(b.Spans),
		Content:   slices.Clone(b.Content),
		Counts:    slices.Clone(b.Counts),
	}
}
