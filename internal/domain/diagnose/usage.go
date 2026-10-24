// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const (
	heavyInputTokens = 32_000
	heavyFactor      = 4
	minHeavyPeers    = 3
	costShareLimit   = 0.5
	nanosPerUSD      = 1e9
)

func tokenHeavy(spans []query.SpanView) []Finding {
	var calls []int
	for i := range spans {
		if spans[i].Kind == trace.KindLLM && spans[i].HasUsage && !spans[i].UsageDuplicate {
			calls = append(calls, i)
		}
	}
	if len(calls) == 0 {
		return nil
	}
	med := median(calls, func(j int) int64 { return spans[j].Usage.Input })
	var out []Finding
	for _, i := range calls {
		s := &spans[i]
		in := s.Usage.Input
		relative := len(calls) >= minHeavyPeers && med > 0 && in >= heavyFactor*med
		if in < heavyInputTokens && !relative {
			continue
		}
		text := fmt.Sprintf("%s sent %d input tokens", quote(s.Name), in)
		if relative {
			text += fmt.Sprintf(", %.1fx the median call (%d)", float64(in)/float64(med), med)
		}
		out = append(out, Finding{Kind: KindTokenHeavy, Text: text, SpanIDs: []string{s.SpanID}})
	}
	return out
}

func costShare(spans []query.SpanView) []Finding {
	var total int64
	var priced int
	for i := range spans {
		if c := spans[i].CostNanos; c != nil && *c > 0 {
			total += *c
			priced++
		}
	}
	if priced < 2 {
		return nil
	}
	var out []Finding
	for i := range spans {
		s := &spans[i]
		if s.CostNanos == nil || float64(*s.CostNanos) < costShareLimit*float64(total) {
			continue
		}
		out = append(out, Finding{
			Kind: KindCostShare,
			Text: fmt.Sprintf("%s accounts for %s of the trace cost ($%.4f of $%.4f)", quote(s.Name),
				percent(*s.CostNanos, total), float64(*s.CostNanos)/nanosPerUSD, float64(total)/nanosPerUSD),
			SpanIDs: []string{s.SpanID},
		})
	}
	return out
}
