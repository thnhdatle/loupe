// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"context"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type Filter struct {
	Kind trace.Kind
}

type RollupReader interface {
	ReadRollups(ctx context.Context, res Resolution, r Range, f Filter) ([]Row, error)
	ReadUserRollups(ctx context.Context, res Resolution, r Range) ([]UserRow, error)
}
