// SPDX-License-Identifier: Apache-2.0

package ingest

import "github.com/thnhdatle/loupe/internal/domain/trace"

// Fixed overheads of the queue byte estimator.
// The estimate of a queued item is the byte length of what varies (attribute
// JSON, content bodies, strings) plus these constants for what does not.
// The calibration test (internal/adapter/otlp/estimate_calibration_test.go)
// compares the estimate with the measured heap delta on 100k spans of the
// benchmark payload mix (target ±25%; measured 1.13); these constants are what it tunes.
const (
	// SpanOverheadBytes covers the trace.Span value (about 360 bytes on 64-bit
	// platforms), the size-class slack of its attribute and content slices and
	// its share of the batch and queue bookkeeping.
	SpanOverheadBytes = 512
	// PartOverheadBytes covers one trace.ContentPart value (about 130 bytes)
	// and the slack of its body allocation.
	PartOverheadBytes = 160
	// ResourceOverheadBytes covers one trace.Resource value and its slice slot.
	ResourceOverheadBytes = 64
)

// EstimateSpanBytes estimates the memory a queued span holds. Byte slices count
// at capacity, because that is what stays allocated.
func EstimateSpanBytes(s *trace.Span) int64 {
	n := int64(SpanOverheadBytes + cap(s.Attributes) +
		len(s.Service) + len(s.Name) + len(s.Model) + len(s.Provider) +
		len(s.SessionID) + len(s.UserID) + len(s.ErrorType))
	for i := range s.Content {
		n += EstimatePartBytes(&s.Content[i])
	}
	return n
}

// EstimatePartBytes estimates the memory a queued content part holds.
func EstimatePartBytes(p *trace.ContentPart) int64 {
	return int64(PartOverheadBytes + cap(p.Body) + len(p.Key) + len(p.Role))
}

// EstimateResourceBytes estimates the memory a queued resource holds.
func EstimateResourceBytes(r *trace.Resource) int64 {
	return int64(ResourceOverheadBytes + cap(r.Attributes))
}
