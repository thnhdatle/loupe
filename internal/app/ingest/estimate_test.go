// SPDX-License-Identifier: Apache-2.0

package ingest_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"

	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestEstimate_OverheadsCoverStructSizes(t *testing.T) {
	t.Parallel()
	assert.Greater(t, ingest.SpanOverheadBytes, int(unsafe.Sizeof(trace.Span{})))
	assert.Greater(t, ingest.PartOverheadBytes, int(unsafe.Sizeof(trace.ContentPart{})))
	assert.Greater(t, ingest.ResourceOverheadBytes, int(unsafe.Sizeof(trace.Resource{})))
}

func TestEstimateSpanBytes(t *testing.T) {
	t.Parallel()
	attrs := make([]byte, 10, 64)
	body := make([]byte, 100, 128)
	tests := []struct {
		name string
		span trace.Span
		want int64
	}{
		{name: "empty span", want: ingest.SpanOverheadBytes},
		{
			name: "attributes count at capacity, strings at length",
			span: trace.Span{Attributes: attrs, Name: "chat", Model: "m1", Service: "svc"},
			want: ingest.SpanOverheadBytes + 64 + 4 + 2 + 3,
		},
		{
			name: "content parts",
			span: trace.Span{Content: []trace.ContentPart{{Body: body, Key: "k", Role: "user"}, {Key: "marker"}}},
			want: ingest.SpanOverheadBytes + (ingest.PartOverheadBytes + 128 + 1 + 4) + (ingest.PartOverheadBytes + 6),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ingest.EstimateSpanBytes(&tc.span))
		})
	}
}

func TestEstimateResourceBytes(t *testing.T) {
	t.Parallel()
	r := trace.Resource{Attributes: make([]byte, 5, 32)}
	assert.Equal(t, int64(ingest.ResourceOverheadBytes+32), ingest.EstimateResourceBytes(&r))
}
