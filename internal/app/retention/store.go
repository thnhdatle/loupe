// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"context"
	"time"
)

type Class string

const (
	ClassContent       Class = "content"
	ClassOrphanContent Class = "orphan_content"
	ClassSpans         Class = "spans"
	ClassSessions      Class = "sessions"
	ClassRollupsHourly Class = "rollups_hourly"
	ClassRollupsDaily  Class = "rollups_daily"
)

func Classes() []Class {
	return []Class{
		ClassContent, ClassOrphanContent, ClassSpans, ClassSessions,
		ClassRollupsHourly, ClassRollupsDaily,
	}
}

type Store interface {
	Purge(ctx context.Context, c Class, cutoff time.Time, limit int) (int, error)
	Size(ctx context.Context) (int64, error)
	IncrementalVacuum(ctx context.Context, maxPages int) (remaining int, err error)
	Checkpoint(ctx context.Context) (busy bool, err error)
}
