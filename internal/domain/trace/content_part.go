// SPDX-License-Identifier: Apache-2.0

package trace

type ContentSource string

const (
	SourceAttribute ContentSource = "attribute"
	SourceEvent     ContentSource = "event"
	SourceLog       ContentSource = "log"
)

type ContentPart struct {
	TraceID   ID
	SpanID    SpanID
	Part      int
	Source    ContentSource
	Key       string
	Role      string
	Bytes     int
	Body      []byte
	Truncated bool
	TimeNs    int64
}

func (p *ContentPart) IsMarker() bool { return p.Body == nil }
