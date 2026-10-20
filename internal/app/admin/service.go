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

// Defaults of the Service options.
const (
	DefaultBatch   = 5000
	DefaultMaxJobs = 1000
)

// Job names submitted to the writer; a fixed vocabulary.
const (
	jobRecompute  = "admin_recompute"
	jobDeleteUser = "admin_delete_user"
	jobVacuum     = "admin_vacuum"
	jobCheckpoint = "admin_checkpoint"
)

// Service runs the admin operations as asynchronous jobs. It is safe for
// concurrent use.
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
	mu     sync.Mutex // protects closed
	closed bool
}

// Option configures a Service.
type Option func(*Service)

// WithBatch sets the maximum rows per writer job (default DefaultBatch).
func WithBatch(n int) Option { return func(s *Service) { s.batch = n } }

// WithMaxJobs sets how many jobs the status registry keeps (default DefaultMaxJobs).
func WithMaxJobs(n int) Option { return func(s *Service) { s.jobs = newRegistry(n) } }

// WithIDGenerator replaces the random job id generator.
func WithIDGenerator(f func() string) Option { return func(s *Service) { s.newID = f } }

// New returns a Service. Writes go through runner, backups through b. Close it
// to stop running jobs.
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

// Recompute starts repricing of stored spans in r with calc and returns the job id.
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

// Vacuum starts an incremental vacuum followed by a checkpoint and returns the job id.
func (s *Service) Vacuum() (string, error) {
	return s.start(KindVacuum, s.vacuum)
}

// Backup starts a backup to path and returns the job id. The path must not exist yet.
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

// BackupPath returns the default backup file under dir/backups for time now:
// loupe-<UTC timestamp>.db.
func BackupPath(dir string, now time.Time) string {
	return filepath.Join(dir, "backups", "loupe-"+now.UTC().Format("20060102T150405Z")+".db")
}

// JobStatus returns the status of job id, or ErrJobNotFound.
func (s *Service) JobStatus(id string) (JobStatus, error) { return s.jobs.get(id) }

// Close rejects new operations, cancels running jobs and waits for them.
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	return nil
}

// start registers a job and runs run in a goroutine owned by the service.
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
	_, _ = rand.Read(b[:]) // never fails on supported platforms
	return "job_" + hex.EncodeToString(b[:])
}
