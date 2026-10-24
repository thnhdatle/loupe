// SPDX-License-Identifier: Apache-2.0

package ingest

import "github.com/thnhdatle/loupe/internal/domain/trace"

const (
	SpanOverheadBytes     = 512
	PartOverheadBytes     = 160
	ResourceOverheadBytes = 64
)

func EstimateSpanBytes(s *trace.Span) int64 {
	n := int64(SpanOverheadBytes + cap(s.Attributes) +
		len(s.Service) + len(s.Name) + len(s.Model) + len(s.Provider) +
		len(s.SessionID) + len(s.UserID) + len(s.ErrorType))
	for i := range s.Content {
		n += EstimatePartBytes(&s.Content[i])
	}
	return n
}

func EstimatePartBytes(p *trace.ContentPart) int64 {
	return int64(PartOverheadBytes + cap(p.Body) + len(p.Key) + len(p.Role))
}

func EstimateResourceBytes(r *trace.Resource) int64 {
	return int64(ResourceOverheadBytes + cap(r.Attributes))
}
