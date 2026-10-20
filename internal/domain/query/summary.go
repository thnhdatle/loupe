// SPDX-License-Identifier: Apache-2.0

package query

import (
	"encoding/json"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// TraceSummary is one row of a trace list.
type TraceSummary struct {
	TraceID       string      `json:"trace_id"`
	RootName      string      `json:"root_name"`
	Service       string      `json:"service"` // service of the root span
	StartNs       int64       `json:"start_ns"`
	EndNs         int64       `json:"end_ns"`
	DurationNs    int64       `json:"duration_ns"`
	SpanCount     int         `json:"span_count"`
	Models        []string    `json:"models"`
	Usage         trace.Usage `json:"usage"`
	CostNanos     int64       `json:"cost_nanos"`
	UnpricedSpans int         `json:"unpriced_spans"`
	HasError      bool        `json:"has_error"`
	SessionID     string      `json:"session_id"`
	UserID        string      `json:"user_id"`
}

// TracePage is one page of trace summaries; NextCursor is empty on the last page.
type TracePage struct {
	Data       []TraceSummary `json:"data"`
	NextCursor string         `json:"next_cursor"`
}

// SessionSummary is one row of a session list.
type SessionSummary struct {
	ID         string      `json:"id"`
	UserID     string      `json:"user_id"`
	FirstNs    int64       `json:"first_ns"`
	LastNs     int64       `json:"last_ns"`
	TraceCount int         `json:"trace_count"`
	Usage      trace.Usage `json:"usage"`
	CostNanos  int64       `json:"cost_nanos"`
}

// SessionPage is one page of session summaries; NextCursor is empty on the last page.
type SessionPage struct {
	Data       []SessionSummary `json:"data"`
	NextCursor string           `json:"next_cursor"`
}

// SessionDetail is a session with its traces ordered by start time (ascending).
type SessionDetail struct {
	Summary    SessionSummary `json:"summary"`
	Data       []TraceSummary `json:"data"`
	NextCursor string         `json:"next_cursor"`
}

// ContentPartView is a stored content part or marker of a span.
type ContentPartView struct {
	Role      string              `json:"role"`
	Bytes     int                 `json:"bytes"`
	Body      *string             `json:"body,omitempty"` // nil for a marker or when content is not requested
	Truncated bool                `json:"truncated"`
	Source    trace.ContentSource `json:"source"`
}

// SpanView is a span with typed fields and decoded flags.
type SpanView struct {
	SpanID          string            `json:"span_id"`
	ParentSpanID    string            `json:"parent_span_id"`
	Service         string            `json:"service"`
	Name            string            `json:"name"`
	Kind            trace.Kind        `json:"kind"`
	Status          trace.Status      `json:"status"`
	StartNs         int64             `json:"start_ns"`
	EndNs           int64             `json:"end_ns"`
	DurationNs      int64             `json:"duration_ns"`
	Model           string            `json:"model"`
	Provider        string            `json:"provider"`
	Usage           trace.Usage       `json:"usage"`
	HasUsage        bool              `json:"has_usage"`
	CostNanos       *int64            `json:"cost_nanos"` // nil when unpriced
	SessionID       string            `json:"session_id"`
	UserID          string            `json:"user_id"`
	ErrorType       string            `json:"error_type"`
	AttrTruncated   bool              `json:"attr_truncated"`
	UsageDuplicate  bool              `json:"usage_duplicate"`
	UsageClamped    bool              `json:"usage_clamped"`
	Attributes      json.RawMessage   `json:"attributes"`
	Content         []ContentPartView `json:"content,omitempty"`
	ContentStripped bool              `json:"content_stripped"`
}

// TopSpan is one entry of the top-spans list: a span and the trace it belongs to.
type TopSpan struct {
	TraceID string   `json:"trace_id"`
	Span    SpanView `json:"span"`
}

// TraceDetail is a trace summary with its spans.
type TraceDetail struct {
	Summary TraceSummary `json:"summary"`
	Spans   []SpanView   `json:"spans"`
}

// IngestStats is the data behind GET /v1/ingest/stats.
type IngestStats struct {
	Received        int64            `json:"received"`
	Stored          int64            `json:"stored"`
	Duplicates      int64            `json:"duplicates"`
	Rejected        map[string]int64 `json:"rejected"`  // by reason
	Dropped         map[string]int64 `json:"dropped"`   // by reason
	Discarded       map[string]int64 `json:"discarded"` // by signal
	ContentStripped int64            `json:"content_stripped"`
	Unpriced        int64            `json:"unpriced"`
	QueueSpans      int64            `json:"queue_spans"`
	QueueBytes      int64            `json:"queue_bytes"`
	InflightBytes   int64            `json:"inflight_bytes"`
}

// NewSpanView converts a stored span into its view, decoding flags and
// mapping content parts. Bodies are included only when includeContent is true.
func NewSpanView(s *trace.Span, includeContent bool) SpanView {
	v := SpanView{
		SpanID: s.SpanID.String(), Service: s.Service, Name: s.Name, Kind: s.Kind, Status: s.Status,
		StartNs: s.StartNs, EndNs: s.EndNs, DurationNs: s.DurationNs(), Model: s.Model,
		Provider: s.Provider, Usage: s.Usage, HasUsage: s.HasUsage, SessionID: s.SessionID,
		UserID: s.UserID, ErrorType: s.ErrorType,
		AttrTruncated:   s.Flags.Has(trace.FlagAttrTruncated),
		UsageDuplicate:  s.Flags.Has(trace.FlagUsageDuplicate),
		UsageClamped:    s.Flags.Has(trace.FlagUsageClamped),
		Attributes:      json.RawMessage(s.Attributes),
		ContentStripped: s.ContentStripped,
	}
	if !s.IsRoot() {
		v.ParentSpanID = s.ParentSpanID.String()
	}
	if s.Cost.Priced {
		n := s.Cost.Nanos
		v.CostNanos = &n
	}
	for i := range s.Content {
		p := &s.Content[i]
		cv := ContentPartView{Role: p.Role, Bytes: p.Bytes, Truncated: p.Truncated, Source: p.Source}
		if includeContent && !p.IsMarker() {
			body := string(p.Body)
			cv.Body = &body
		}
		v.Content = append(v.Content, cv)
	}
	return v
}
