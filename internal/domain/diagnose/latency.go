// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"fmt"
	"slices"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const (
	minSlowNs      = int64(100 * time.Millisecond)
	dominantShare  = 0.5
	outlierFactor  = 3
	minOutlierPeer = 3
)

func latency(spans []query.SpanView) []Finding {
	leaves := leafSpans(spans)
	total := traceDuration(spans)
	var out []Finding
	flagged := map[int]bool{}
	for _, i := range leaves {
		s := &spans[i]
		if len(leaves) < minOutlierPeer || s.DurationNs < minSlowNs || float64(s.DurationNs) < dominantShare*float64(total) {
			continue
		}
		flagged[i] = true
		out = append(out, Finding{
			Kind: KindSlow,
			Text: fmt.Sprintf("%s took %s, %s of the trace", quote(s.Name), formatDuration(s.DurationNs),
				percent(s.DurationNs, total)),
			SpanIDs: []string{s.SpanID},
		})
	}
	byKind := map[trace.Kind][]int{}
	for _, i := range leaves {
		byKind[spans[i].Kind] = append(byKind[spans[i].Kind], i)
	}
	for _, i := range leaves {
		s := &spans[i]
		peers := byKind[s.Kind]
		if flagged[i] || len(peers) < minOutlierPeer || s.DurationNs < minSlowNs {
			continue
		}
		med := median(peers, func(j int) int64 { return spans[j].DurationNs })
		if med <= 0 || s.DurationNs < outlierFactor*med {
			continue
		}
		out = append(out, Finding{
			Kind: KindSlow,
			Text: fmt.Sprintf("%s took %s, %.1fx the median %s span (%s)", quote(s.Name), formatDuration(s.DurationNs),
				float64(s.DurationNs)/float64(med), s.Kind, formatDuration(med)),
			SpanIDs: []string{s.SpanID},
		})
	}
	return out
}

func leafSpans(spans []query.SpanView) []int {
	parents := make(map[string]bool, len(spans))
	for i := range spans {
		parents[spans[i].ParentSpanID] = true
	}
	var out []int
	for i := range spans {
		if !parents[spans[i].SpanID] {
			out = append(out, i)
		}
	}
	return out
}

func traceDuration(spans []query.SpanView) int64 {
	if len(spans) == 0 {
		return 0
	}
	lo, hi := spans[0].StartNs, spans[0].EndNs
	for i := range spans {
		lo, hi = min(lo, spans[i].StartNs), max(hi, spans[i].EndNs)
	}
	return hi - lo
}

func median(idx []int, val func(int) int64) int64 {
	vals := make([]int64, 0, len(idx))
	for _, i := range idx {
		vals = append(vals, val(i))
	}
	slices.Sort(vals)
	n := len(vals)
	if n%2 == 0 {
		return (vals[n/2-1] + vals[n/2]) / 2
	}
	return vals[n/2]
}

func percent(part, total int64) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(part)/float64(total))
}

func formatDuration(ns int64) string { return time.Duration(ns).Round(time.Millisecond).String() }
