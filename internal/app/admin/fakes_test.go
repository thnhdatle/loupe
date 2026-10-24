// SPDX-License-Identifier: Apache-2.0

package admin_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/testutil"
)

var epoch = time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)

type fakeRunner struct {
	mu    sync.Mutex
	names []string
	err   map[string]error
	gate  chan struct{}
}

func (r *fakeRunner) Submit(ctx context.Context, j trace.Job) error {
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	r.names = append(r.names, j.Name)
	err := r.err[j.Name]
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return j.Run(ctx)
}

func (r *fakeRunner) jobNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

type fakeStore struct {
	mu         sync.Mutex
	recompute  int
	deleteLeft int
	freeLeft   int
	afterKeys  []int64
	limits     []int
	checkpoint int
	vacuumed   int
	errOn      string
}

func (s *fakeStore) RecomputeCosts(
	_ context.Context, _ *cost.Calculator, _ admin.Range, after int64, limit int,
) (int64, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errOn == "recompute" {
		return 0, 0, errBoom
	}
	s.afterKeys = append(s.afterKeys, after)
	s.limits = append(s.limits, limit)
	n := min(limit, s.recompute)
	s.recompute -= n
	return after + int64(n), n, nil
}

func (s *fakeStore) DeleteUserData(_ context.Context, _ string, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errOn == "delete" {
		return 0, errBoom
	}
	n := min(limit, s.deleteLeft)
	s.deleteLeft -= n
	return n, nil
}

func (s *fakeStore) IncrementalVacuum(_ context.Context, pages int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errOn == "vacuum" {
		return 0, errBoom
	}
	n := min(pages, s.freeLeft)
	s.freeLeft -= n
	s.vacuumed += n
	return n, nil
}

func (s *fakeStore) Checkpoint(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errOn == "checkpoint" {
		return errBoom
	}
	s.checkpoint++
	return nil
}

type fakeBackuper struct {
	mu    sync.Mutex
	paths []string
	err   error
}

func (b *fakeBackuper) BackupTo(_ context.Context, path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.paths = append(b.paths, path)
	return b.err
}

type env struct {
	svc    *admin.Service
	store  *fakeStore
	runner *fakeRunner
	backup *fakeBackuper
}

func newEnv(t *testing.T, st *fakeStore, opts ...admin.Option) *env {
	t.Helper()
	e := &env{store: st, runner: &fakeRunner{}, backup: &fakeBackuper{}}
	var n int
	opts = append([]admin.Option{admin.WithBatch(10), admin.WithIDGenerator(func() string {
		n++
		return "j" + string(rune('0'+n))
	})}, opts...)
	e.svc = admin.New(st, e.backup, e.runner, clock.NewFake(epoch), opts...)
	t.Cleanup(func() { require.NoError(t, e.svc.Close()) })
	return e
}

func (e *env) await(t *testing.T, id string) admin.JobStatus {
	t.Helper()
	var st admin.JobStatus
	testutil.Eventually(t, func() bool {
		var err error
		st, err = e.svc.JobStatus(id)
		return err == nil && (st.State == admin.StateDone || st.State == admin.StateFailed)
	}, testutil.DefaultTimeout)
	return st
}
