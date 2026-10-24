// SPDX-License-Identifier: Apache-2.0

package query

import (
	"encoding/json"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type TraceSummary struct {
	TraceID       string      `json:"trace_id"`
	RootName      string      `json:"root_name"`
	Service       string      `json:"service"`
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

type TracePage struct {
	Data       []TraceSummary `json:"data"`
	NextCursor string         `json:"next_cursor"`
}

type SessionSummary struct {
	ID         string      `json:"id"`
	UserID     string      `json:"user_id"`
	FirstNs    int64       `json:"first_ns"`
	LastNs     int64       `json:"last_ns"`
	TraceCount int         `json:"trace_count"`
	Usage      trace.Usage `json:"usage"`
	CostNanos  int64       `json:"cost_nanos"`
}

type SessionPage struct {
	Data       []SessionSummary `json:"data"`
	NextCursor string           `json:"next_cursor"`
}

type SessionDetail struct {
	Summary    SessionSummary `json:"summary"`
	Data       []TraceSummary `json:"data"`
	NextCursor string         `json:"next_cursor"`
}

type ContentPartView struct {
	Role      string              `json:"role"`
	Bytes     int                 `json:"bytes"`
	Body      *string             `json:"body,omitempty"`
	Truncated bool                `json:"truncated"`
	Source    trace.ContentSource `json:"source"`
}

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
	CostNanos       *int64            `json:"cost_nanos"`
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

type TopSpan struct {
	TraceID string   `json:"trace_id"`
	Span    SpanView `json:"span"`
}

type TraceDetail struct {
	Summary TraceSummary `json:"summary"`
	Spans   []SpanView   `json:"spans"`
}

type IngestStats struct {
	Received        int64            `json:"received"`
	Stored          int64            `json:"stored"`
	Duplicates      int64            `json:"duplicates"`
	Rejected        map[string]int64 `json:"rejected"`
	Dropped         map[string]int64 `json:"dropped"`
	Discarded       map[string]int64 `json:"discarded"`
	ContentStripped int64            `json:"content_stripped"`
	Unpriced        int64            `json:"unpriced"`
	QueueSpans      int64            `json:"queue_spans"`
	QueueBytes      int64            `json:"queue_bytes"`
	InflightBytes   int64            `json:"inflight_bytes"`
}

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
