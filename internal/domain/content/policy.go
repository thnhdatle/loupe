// SPDX-License-Identifier: Apache-2.0

package content

import (
	"unicode/utf8"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Policy is the content capture policy of one deployment.
type Policy struct {
	Mode     Mode
	MaxBytes int       // per-span body budget in ModeOn (see Budget); <= 0 means no limit
	Redactor *Redactor // may be nil
}

// Apply enforces the policy on the content parts of one span. Input parts carry
// Body text; a zero Bytes defaults to len(Body). The input is not modified.
// stripped reports that content arrived but no text will be stored (off and
// metadata_only). An unknown mode is treated as ModeOff.
func Apply(p Policy, parts []trace.ContentPart) (out []trace.ContentPart, stripped bool) {
	if len(parts) == 0 {
		return nil, false
	}
	switch p.Mode {
	case ModeOn:
		return applyOn(p, parts), false
	case ModeMetadataOnly:
		out = make([]trace.ContentPart, len(parts))
		for i := range parts {
			out[i] = parts[i]
			out[i].Bytes = originalBytes(&parts[i])
			out[i].Body = nil
			out[i].Truncated = false
		}
		return out, true
	case ModeOff:
	}
	first := parts[0]
	total := 0
	for i := range parts {
		total += originalBytes(&parts[i])
	}
	return []trace.ContentPart{{
		TraceID: first.TraceID, SpanID: first.SpanID, Source: first.Source, Key: first.Key,
		Bytes: total,
	}}, true
}

func originalBytes(p *trace.ContentPart) int {
	if p.Bytes == 0 {
		return len(p.Body)
	}
	return p.Bytes
}

func applyOn(p Policy, parts []trace.ContentPart) []trace.ContentPart {
	out := make([]trace.ContentPart, len(parts))
	b := NewBudget(p.MaxBytes, 0, false)
	for i := range parts {
		part := parts[i]
		part.Bytes = originalBytes(&parts[i])
		out[i] = part
		if part.Body == nil {
			continue
		}
		body, truncated := b.Take(p.Redactor.RedactBytes(part.Body))
		out[i].Body = body
		out[i].Truncated = part.Truncated || truncated
	}
	return out
}

// Budget is the content.max_bytes budget of one span in ModeOn. It
// counts stored (redacted) body bytes; the part that crosses the limit is cut
// at a rune boundary and every later non-empty part is stored empty, both
// marked truncated. Apply uses one Budget per call; the store keeps one per
// span across batches so the limit holds whatever the arrival order.
type Budget struct {
	limit     int // <= 0: no limit
	remaining int
	exhausted bool
}

// NewBudget returns the budget of a span that already stores used body bytes;
// exhausted reports that one of its stored parts was truncated.
func NewBudget(limit, used int, exhausted bool) Budget {
	return Budget{limit: limit, remaining: max(limit-used, 0), exhausted: exhausted}
}

// Take fits body into the budget and returns the body to store and whether it
// was truncated.
func (b *Budget) Take(body []byte) ([]byte, bool) {
	switch {
	case b.limit <= 0 || !b.exhausted && len(body) <= b.remaining:
		b.remaining -= len(body)
		return body, false
	case b.exhausted:
		return []byte{}, true
	default:
		n := cutIndex(body, b.remaining)
		b.remaining, b.exhausted = 0, true
		return body[:n:n], true
	}
}

// Exhaust ends the budget, e.g. after a part that was truncated upstream.
func (b *Budget) Exhaust() { b.remaining, b.exhausted = 0, true }

// cutIndex returns the largest index <= limit that does not split a UTF-8 rune.
func cutIndex(b []byte, limit int) int {
	if limit >= len(b) {
		return len(b)
	}
	for limit > 0 && !utf8.RuneStart(b[limit]) {
		limit--
	}
	return limit
}
