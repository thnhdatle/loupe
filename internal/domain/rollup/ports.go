// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"context"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Filter narrows rollup rows. The zero value selects every row.
type Filter struct {
	Kind trace.Kind // empty: every kind
}

// RollupReader reads decoded rollup rows. Implemented by the storage adapter,
// consumed by app/queries, which plans a range with PlanRange, reads the day
// rows of Plan.Days and the hour rows of each Plan.Hours range, and merges them
// with Assemble or MergeBy.
//
// Both methods return the rows of resolution res whose bucket start lies in r,
// in no particular order. User rollups have no kind dimension, so they take no
// filter.
//
//nolint:revive // exported: RollupReader is the established port name.
type RollupReader interface {
	ReadRollups(ctx context.Context, res Resolution, r Range, f Filter) ([]Row, error)
	ReadUserRollups(ctx context.Context, res Resolution, r Range) ([]UserRow, error)
}
