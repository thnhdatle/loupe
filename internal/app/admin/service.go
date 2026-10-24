// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const (
	DefaultBatch   = 5000
	DefaultMaxJobs = 1000
)

const (
	jobRecompute  = "admin_recompute"
	jobDeleteUser = "admin_delete_user"
	jobVacuum     = "admin_vacuum"
	jobCheckpoint = "admin_checkpoint"
)

// Service runs the admin operations as asynchronous jobs.
type Service struct {
	store  Store
	backup Backuper
	runner trace.JobRunner
	clock  clock.Clock
	batch  int
	newID  func() string
	jobs   *registry

	ctx    context.Context //nolint:containedctx // lifetime context of the service-owned job goroutines
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

type Option func(*Service)

func WithBatch(n int) Option { return func(s *Service) { s.batch = n } }

func WithMaxJobs(n int) Option { return func(s *Service) { s.jobs = newRegistry(n) } }

func WithIDGenerator(f func() string) Option { return func(s *Service) { s.newID = f } }

// New returns a Service that writes through runner and backs up through b.
func New(st Store, b Backuper, runner trace.JobRunner, clk clock.Clock, opts ...Option) *Service {
	s := &Service{
		store: st, backup: b, runner: runner, clock: clk,
		batch: DefaultBatch, newID: randomID, jobs: newRegistry(DefaultMaxJobs),
	}
	for _, o := range opts {
		o(s)
	}
	if s.batch < 1 {
		s.batch = DefaultBatch
	}
	if s.jobs.max < 1 {
		s.jobs = newRegistry(DefaultMaxJobs)
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s
}

// Recompute starts repricing of the stored spans in r and returns the job id.
func (s *Service) Recompute(calc *cost.Calculator, r Range) (string, error) {
	if calc == nil {
		return "", cost.ErrNilTable
	}
	if !r.Since.IsZero() && !r.Until.IsZero() && r.Since.After(r.Until) {
		return "", ErrInvalidRange
	}
	return s.start(KindRecompute, func(ctx context.Context, add func(int)) error {
		return s.recompute(ctx, calc, r, add)
	})
}

// DeleteUser starts deletion of all data of userID and returns the job id.
func (s *Service) DeleteUser(userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", ErrEmptyUserID
	}
	return s.start(KindUserDelete, func(ctx context.Context, add func(int)) error {
		return s.deleteUser(ctx, userID, add)
	})
}

// Vacuum starts an incremental vacuum and returns the job id.
func (s *Service) Vacuum() (string, error) {
	return s.start(KindVacuum, s.vacuum)
}

// Backup starts a backup to path and returns the job id.
func (s *Service) Backup(path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return "", ErrInvalidPath
	}
	return s.start(KindBackup, func(ctx context.Context, _ func(int)) error {
		if err := s.backup.BackupTo(ctx, path); err != nil {
			return fmt.Errorf("admin: backup: %w", err)
		}
		return nil
	})
}

func BackupPath(dir string, now time.Time) string {
	return filepath.Join(dir, "backups", "loupe-"+now.UTC().Format("20060102T150405Z")+".db")
}

func (s *Service) JobStatus(id string) (JobStatus, error) { return s.jobs.get(id) }

func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	return nil
}

func (s *Service) start(kind Kind, run func(ctx context.Context, add func(int)) error) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrClosed
	}
	id := s.newID()
	if err := s.jobs.add(JobStatus{ID: id, Kind: kind, State: StatePending, CreatedAt: s.clock.Now()}); err != nil {
		return "", err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.jobs.update(id, func(j *JobStatus) { j.State, j.StartedAt = StateRunning, s.clock.Now() })
		err := run(s.ctx, func(n int) { s.jobs.update(id, func(j *JobStatus) { j.Rows += n }) })
		s.jobs.update(id, func(j *JobStatus) {
			j.FinishedAt = s.clock.Now()
			if err != nil {
				j.State, j.Error = StateFailed, err.Error()
				return
			}
			j.State = StateDone
		})
	}()
	return id, nil
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "job_" + hex.EncodeToString(b[:])
}
