// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type callKey struct {
	parent string
	model  string
}

// retries reports LLM calls repeated with the same model under one parent when at least one of them failed;
// repeated calls that all succeed are normal agent loops.
func retries(spans []query.SpanView) []Finding {
	var keys []callKey
	calls := map[callKey][]int{}
	for i := range spans {
		s := &spans[i]
		if s.Kind != trace.KindLLM || s.Model == "" {
			continue
		}
		k := callKey{parent: s.ParentSpanID, model: s.Model}
		if _, ok := calls[k]; !ok {
			keys = append(keys, k)
		}
		calls[k] = append(calls[k], i)
	}
	var out []Finding
	for _, k := range keys {
		idx := calls[k]
		if len(idx) < 2 {
			continue
		}
		var failed int
		var wasted int64
		ids := make([]string, 0, len(idx))
		for _, i := range idx {
			ids = append(ids, spans[i].SpanID)
			if spans[i].Status == trace.StatusError {
				failed++
				wasted += spans[i].DurationNs
			}
		}
		if failed == 0 {
			continue
		}
		outcome := "the last call succeeded"
		if spans[idx[len(idx)-1]].Status == trace.StatusError {
			outcome = "the last call failed too"
		}
		out = append(out, Finding{
			Kind: KindRetry,
			Text: fmt.Sprintf("%d calls to %s under one parent, %d failed (likely retries, %s spent on failed calls); %s",
				len(idx), k.model, failed, formatDuration(wasted), outcome),
			SpanIDs: ids,
		})
	}
	return out
}
