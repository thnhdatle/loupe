// SPDX-License-Identifier: Apache-2.0

package trace

// ContentSource says where a content part came from.
type ContentSource string

// Content sources (span_content.source).
const (
	SourceAttribute ContentSource = "attribute"
	SourceEvent     ContentSource = "event"
	SourceLog       ContentSource = "log"
)

// ContentPart is one prompt, response, tool argument/result or document fragment.
// A part with a nil Body is a content marker: content arrived but was not stored.
type ContentPart struct {
	TraceID   ID
	SpanID    SpanID
	Part      int // order within the span, assigned at ingest
	Source    ContentSource
	Key       string // attribute key or event name it came from
	Role      string // e.g. system, user, assistant, tool; empty when unknown
	Bytes     int    // original length in bytes, before truncation
	Body      []byte // plain text before compression; nil for a marker
	Truncated bool
	TimeNs    int64 // event or log time; 0 for attributes
}

// IsMarker reports whether the part carries no body.
func (p *ContentPart) IsMarker() bool { return p.Body == nil }
