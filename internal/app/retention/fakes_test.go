// SPDX-License-Identifier: Apache-2.0

package retention_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const pageSize = 4096

// Fake tables. Rows carry their own size so that the fake can model the
// database file: deleted bytes move to the free list and the WAL until
// IncrementalVacuum and Checkpoint release them.
type (
	fakeSpan struct {
		id, session string
		start       time.Time
		bytes       int64
	}
	fakeContent struct {
		span  string
		at    time.Time
		bytes int64
	}
	fakeSession struct {
		id   string
		last time.Time
	}
	fakeBucket struct {
		table string
		start time.Time
		width time.Duration
	}
)

type purgeCall struct {
	class  retention.Class
	cutoff time.Time
	limit  int
	n      int
}

// fakeStore is an in-memory retention.Store. It is safe for concurrent use.
type fakeStore struct {
	mu       sync.Mutex // guards every field below
	spans    []fakeSpan
	content  []fakeContent
	sessions []fakeSession
	buckets  []fakeBucket
	live     int64 // bytes of stored rows
	free     int64 // bytes on the free list (always whole pages)
	wal      int64 // bytes in the WAL

	purgeErr   map[retention.Class]error
	sizeErr    error
	vacuumErr  error
	checkErr   error
	busy       bool
	stuckPages int                     // when > 0, IncrementalVacuum always reports this many pages left
	onPurge    func(c retention.Class) // called at the start of each Purge, with mu held

	purges      []purgeCall
	sizeCalls   int
	vacuumCalls int
}

var _ retention.Store = (*fakeStore)(nil)

func newFakeStore() *fakeStore { return &fakeStore{purgeErr: map[retention.Class]error{}} }

func (s *fakeStore) addSpan(id, session string, start time.Time, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, fakeSpan{id: id, session: session, start: start, bytes: size})
	s.live += size
}

func (s *fakeStore) addContent(span string, at time.Time, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.content = append(s.content, fakeContent{span: span, at: at, bytes: size})
	s.live += size
}

func (s *fakeStore) addSession(id string, last time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, fakeSession{id: id, last: last})
	s.live += pageSize
}

func (s *fakeStore) addBucket(table string, start time.Time, width time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buckets = append(s.buckets, fakeBucket{table: table, start: start, width: width})
	s.live += pageSize
}

func (s *fakeStore) set(fn func(s *fakeStore)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func (s *fakeStore) spanIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.spans))
	for _, r := range s.spans {
		ids = append(ids, r.id)
	}
	return ids
}

func (s *fakeStore) contentSpans() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.content))
	for _, r := range s.content {
		ids = append(ids, r.span)
	}
	return ids
}

func (s *fakeStore) sessionIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.sessions))
	for _, r := range s.sessions {
		ids = append(ids, r.id)
	}
	return ids
}

func (s *fakeStore) bucketTables() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.buckets))
	for _, r := range s.buckets {
		out = append(out, r.table)
	}
	return out
}

func (s *fakeStore) purgeCalls() []purgeCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.purges)
}

func (s *fakeStore) counts() (sizeCalls, vacuumCalls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sizeCalls, s.vacuumCalls
}

// sizeNow is the modelled file size: live rows, free list and WAL.
func (s *fakeStore) sizeNow() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live + s.free + s.wal
}

// drop removes up to limit rows of rows matching ok, oldest first, and
// returns the survivors and the removed rows.
func drop[T any](rows []T, at func(T) time.Time, ok func(T) bool, limit int) (kept, gone []T) {
	var cand []int
	for i, r := range rows {
		if ok(r) {
			cand = append(cand, i)
		}
	}
	slices.SortStableFunc(cand, func(a, b int) int { return at(rows[a]).Compare(at(rows[b])) })
	del := make(map[int]bool)
	for _, i := range cand[:min(limit, len(cand))] {
		del[i] = true
	}
	for i, r := range rows {
		if del[i] {
			gone = append(gone, r)
		} else {
			kept = append(kept, r)
		}
	}
	return kept, gone
}

func (s *fakeStore) release(n int64) {
	s.live -= n
	s.free += n
	s.wal += n
}

func (s *fakeStore) Purge(ctx context.Context, c retention.Class, cutoff time.Time, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onPurge != nil {
		s.onPurge(c)
	}
	if err := s.purgeErr[c]; err != nil {
		return 0, err
	}
	n := 0
	switch c {
	case retention.ClassContent, retention.ClassOrphanContent:
		n = s.purgeContent(c, cutoff, limit)
	case retention.ClassSpans:
		n = s.purgeSpans(cutoff, limit)
	case retention.ClassSessions:
		n = s.purgeSessions(cutoff, limit)
	case retention.ClassRollupsHourly:
		n = s.purgeBuckets(cutoff, limit, "rollup_hour", "rollup_user_hour")
	case retention.ClassRollupsDaily:
		n = s.purgeBuckets(cutoff, limit, "rollup_day", "rollup_user_day", "ingest_stats")
	default:
		return 0, fmt.Errorf("fake: unknown class %q", c)
	}
	s.purges = append(s.purges, purgeCall{class: c, cutoff: cutoff, limit: limit, n: n})
	return n, nil
}

func (s *fakeStore) hasSpan(id string) bool {
	return slices.ContainsFunc(s.spans, func(r fakeSpan) bool { return r.id == id })
}

func (s *fakeStore) purgeContent(c retention.Class, cutoff time.Time, limit int) int {
	kept, gone := drop(s.content, func(r fakeContent) time.Time { return r.at }, func(r fakeContent) bool {
		return r.at.Before(cutoff) && (c == retention.ClassContent || !s.hasSpan(r.span))
	}, limit)
	s.content = kept
	for _, r := range gone {
		s.release(r.bytes)
	}
	return len(gone)
}

// purgeSpans also deletes the content of the deleted spans, as the contract says.
func (s *fakeStore) purgeSpans(cutoff time.Time, limit int) int {
	kept, gone := drop(s.spans, func(r fakeSpan) time.Time { return r.start },
		func(r fakeSpan) bool { return r.start.Before(cutoff) }, limit)
	s.spans = kept
	for _, r := range gone {
		s.release(r.bytes)
		var keptContent []fakeContent
		for _, c := range s.content {
			if c.span == r.id {
				s.release(c.bytes)
			} else {
				keptContent = append(keptContent, c)
			}
		}
		s.content = keptContent
	}
	return len(gone)
}

func (s *fakeStore) purgeSessions(cutoff time.Time, limit int) int {
	kept, gone := drop(s.sessions, func(r fakeSession) time.Time { return r.last }, func(r fakeSession) bool {
		used := slices.ContainsFunc(s.spans, func(sp fakeSpan) bool { return sp.session == r.id })
		return !used && r.last.Before(cutoff)
	}, limit)
	s.sessions = kept
	s.release(int64(len(gone)) * pageSize)
	return len(gone)
}

func (s *fakeStore) purgeBuckets(cutoff time.Time, limit int, tables ...string) int {
	kept, gone := drop(s.buckets, func(r fakeBucket) time.Time { return r.start }, func(r fakeBucket) bool {
		return slices.Contains(tables, r.table) && !r.start.Add(r.width).After(cutoff)
	}, limit)
	s.buckets = kept
	s.release(int64(len(gone)) * pageSize)
	return len(gone)
}

func (s *fakeStore) Size(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizeCalls++
	if s.sizeErr != nil {
		return 0, s.sizeErr
	}
	return s.live + s.free + s.wal, nil
}

func (s *fakeStore) IncrementalVacuum(_ context.Context, maxPages int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vacuumCalls++
	if s.vacuumErr != nil {
		return 0, s.vacuumErr
	}
	if s.stuckPages > 0 {
		return s.stuckPages, nil
	}
	freed := min(s.free, int64(maxPages)*pageSize)
	s.free -= freed
	return int((s.free + pageSize - 1) / pageSize), nil
}

func (s *fakeStore) Checkpoint(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkErr != nil {
		return false, s.checkErr
	}
	if s.busy {
		return true, nil
	}
	s.wal = 0
	return false, nil
}

// fakeRunner is a trace.JobRunner that runs jobs one at a time on the
// submitting goroutine, like the writer runs them on its own.
type fakeRunner struct {
	mu       sync.Mutex // serialises jobs, like the single writer
	names    []string   // guarded by mu
	shutdown bool       // guarded by mu; Submit returns trace.ErrShuttingDown
	// block, when set, is called before each job with the job's context; the
	// job runs only if it returns nil.
	block func(ctx context.Context, name string) error
}

var _ trace.JobRunner = (*fakeRunner)(nil)

func (r *fakeRunner) Submit(ctx context.Context, j trace.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shutdown {
		return trace.ErrShuttingDown
	}
	r.names = append(r.names, j.Name)
	if r.block != nil {
		if err := r.block(ctx, j.Name); err != nil {
			return err
		}
	}
	if err := j.Run(ctx); err != nil {
		return fmt.Errorf("fake runner: job %s: %w", j.Name, err)
	}
	return nil
}

func (r *fakeRunner) jobNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.names)
}

func (r *fakeRunner) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shutdown = true
}

// logBuffer is an io.Writer safe for a logger in another goroutine.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
