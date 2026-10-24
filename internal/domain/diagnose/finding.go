// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"cmp"
	"slices"

	"github.com/thnhdatle/loupe/internal/domain/query"
)

type Kind string

const (
	KindRateLimit  Kind = "rate_limit"
	KindToolError  Kind = "tool_error"
	KindError      Kind = "error"
	KindRetry      Kind = "retry"
	KindSlow       Kind = "slow"
	KindTokenHeavy Kind = "token_heavy"
	KindCostShare  Kind = "cost_share"
)

// Finding is one fact about a trace with the spans it is based on.
type Finding struct {
	Kind    Kind     `json:"kind"`
	Text    string   `json:"text"`
	SpanIDs []string `json:"span_ids"`
}

// Find returns the findings for the spans of one trace, most severe first.
func Find(spans []query.SpanView) []Finding {
	spans = byStart(spans)
	return slices.Concat(failures(spans), retries(spans), latency(spans), tokenHeavy(spans), costShare(spans))
}

func byStart(spans []query.SpanView) []query.SpanView {
	out := slices.Clone(spans)
	slices.SortStableFunc(out, func(a, b query.SpanView) int { return cmp.Compare(a.StartNs, b.StartNs) })
	return out
}
