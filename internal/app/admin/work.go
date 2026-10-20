// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// step runs one bounded transaction and reports the rows it handled and whether
// the work is finished.
type step func(ctx context.Context) (n int, done bool, err error)

// loop submits one writer job per step until the work is done, so that ingest
// batches interleave between them.
func (s *Service) loop(ctx context.Context, name string, add func(int), st step) error {
	for {
		var n int
		var done bool
		err := s.runner.Submit(ctx, trace.Job{Name: name, Run: func(ctx context.Context) (err error) {
			n, done, err = st(ctx)
			return err
		}})
		if err != nil {
			return err
		}
		add(n)
		if done {
			return nil
		}
	}
}

// checkpoint runs the final job after work that frees pages: one bounded
// incremental vacuum step, then wal_checkpoint(TRUNCATE).
func (s *Service) checkpoint(ctx context.Context) error {
	return s.runner.Submit(ctx, trace.Job{Name: jobCheckpoint, Run: func(ctx context.Context) error {
		if _, err := s.store.IncrementalVacuum(ctx, s.batch); err != nil {
			return fmt.Errorf("admin: incremental vacuum: %w", err)
		}
		if err := s.store.Checkpoint(ctx); err != nil {
			return fmt.Errorf("admin: checkpoint: %w", err)
		}
		return nil
	}})
}

func (s *Service) recompute(ctx context.Context, calc *cost.Calculator, r Range, add func(int)) error {
	var after int64
	err := s.loop(ctx, jobRecompute, add, func(ctx context.Context) (int, bool, error) {
		next, n, err := s.store.RecomputeCosts(ctx, calc, r, after, s.batch)
		if err != nil {
			return 0, false, fmt.Errorf("admin: recompute costs: %w", err)
		}
		after = next
		return n, n < s.batch, nil
	})
	if err != nil {
		return err
	}
	return s.checkpoint(ctx)
}

func (s *Service) deleteUser(ctx context.Context, userID string, add func(int)) error {
	err := s.loop(ctx, jobDeleteUser, add, func(ctx context.Context) (int, bool, error) {
		n, err := s.store.DeleteUserData(ctx, userID, s.batch)
		if err != nil {
			return 0, false, fmt.Errorf("admin: delete user data: %w", err)
		}
		return n, n == 0, nil
	})
	if err != nil {
		return err
	}
	return s.checkpoint(ctx)
}

func (s *Service) vacuum(ctx context.Context, add func(int)) error {
	err := s.loop(ctx, jobVacuum, add, func(ctx context.Context) (int, bool, error) {
		n, err := s.store.IncrementalVacuum(ctx, s.batch)
		if err != nil {
			return 0, false, fmt.Errorf("admin: incremental vacuum: %w", err)
		}
		return n, n == 0, nil
	})
	if err != nil {
		return err
	}
	return s.checkpoint(ctx)
}
