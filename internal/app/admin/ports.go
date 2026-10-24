// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/cost"
)

type Range struct {
	Since, Until time.Time
}

type Store interface {
	RecomputeCosts(
		ctx context.Context, calc *cost.Calculator, r Range, afterKey int64, limit int,
	) (nextKey int64, n int, err error)
	DeleteUserData(ctx context.Context, userID string, limit int) (n int, err error)
	IncrementalVacuum(ctx context.Context, pages int) (freed int, err error)
	Checkpoint(ctx context.Context) error
}

type Backuper interface {
	BackupTo(ctx context.Context, path string) error
}
