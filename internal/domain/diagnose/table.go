// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"cmp"
	"slices"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const (
	maxPartRunes    = 2000
	maxContentRunes = 24_000
)

// Row is one span in the compact table sent to a model.
type Row struct {
	SpanID       string
	ParentSpanID string
	Name         string
	Kind         trace.Kind
	Model        string
	Status       trace.Status
	StartNs      int64
	DurationNs   int64
	InputTokens  int64
	OutputTokens int64
	CostNanos    *int64
	Error        string
	Content      []Message
}

type Message struct {
	Role string
	Text string
}

// Table returns at most maxRows spans of a trace, ordered by start, and the number left out.
// Spans named in findings and failed spans are kept first; StartNs is relative to the trace start.
func Table(spans []query.SpanView, findings []Finding, maxRows int) ([]Row, int) {
	spans = byStart(spans)
	keep := pick(spans, findings, maxRows)
	var lo int64
	if len(spans) > 0 {
		lo = spans[0].StartNs
	}
	budget := maxContentRunes
	rows := make([]Row, 0, len(keep))
	for _, i := range keep {
		s := &spans[i]
		r := Row{
			SpanID: s.SpanID, ParentSpanID: s.ParentSpanID, Name: s.Name, Kind: s.Kind, Model: s.Model,
			Status: s.Status, StartNs: s.StartNs - lo, DurationNs: s.DurationNs,
			InputTokens: s.Usage.Input, OutputTokens: s.Usage.Output, CostNanos: s.CostNanos, Error: errorText(s),
		}
		r.Content, budget = messages(s.Content, budget)
		rows = append(rows, r)
	}
	return rows, len(spans) - len(keep)
}

func pick(spans []query.SpanView, findings []Finding, maxRows int) []int {
	all := make([]int, len(spans))
	for i := range all {
		all[i] = i
	}
	if len(spans) <= maxRows {
		return all
	}
	cited := map[string]bool{}
	for _, f := range findings {
		for _, id := range f.SpanIDs {
			cited[id] = true
		}
	}
	rank := func(i int) int {
		switch s := &spans[i]; {
		case cited[s.SpanID]:
			return 0
		case s.Status == trace.StatusError:
			return 1
		case s.ParentSpanID == "":
			return 2
		default:
			return 3
		}
	}
	slices.SortStableFunc(all, func(a, b int) int {
		if c := cmp.Compare(rank(a), rank(b)); c != 0 {
			return c
		}
		return cmp.Compare(spans[b].DurationNs, spans[a].DurationNs)
	})
	keep := all[:max(maxRows, 0)]
	slices.Sort(keep)
	return keep
}

func messages(parts []query.ContentPartView, budget int) ([]Message, int) {
	var out []Message
	for _, p := range parts {
		if budget <= 0 {
			break
		}
		if p.Body == nil {
			continue
		}
		text := []rune(*p.Body)
		n := min(len(text), maxPartRunes, budget)
		msg := Message{Role: p.Role, Text: string(text[:n])}
		if n < len(text) {
			msg.Text += "…"
		}
		budget -= n
		out = append(out, msg)
	}
	return out, budget
}
