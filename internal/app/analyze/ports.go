// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"context"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type TraceReader interface {
	GetTrace(ctx context.Context, id trace.ID, includeContent bool) (query.TraceDetail, error)
}

type Prompt struct {
	System string
	User   string
}

// LLM completes one chat prompt and returns the raw answer text.
type LLM interface {
	Complete(ctx context.Context, p Prompt) (string, error)
}
