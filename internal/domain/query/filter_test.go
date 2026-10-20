// SPDX-License-Identifier: Apache-2.0

package query_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestSpanFilter_IsEmpty(t *testing.T) {
	t.Parallel()
	assert.True(t, query.SpanFilter{}.IsEmpty())
	assert.False(t, query.SpanFilter{NamePrefix: "x"}.IsEmpty())
	assert.False(t, query.SpanFilter{MinDurationNs: 1}.IsEmpty())
}

func TestTraceFilter_Validate(t *testing.T) {
	t.Parallel()
	neg := int64(-1)
	zero := int64(0)
	t0 := time.Unix(100, 0)
	tests := []struct {
		name    string
		f       query.TraceFilter
		wantErr []error
	}{
		{"empty", query.TraceFilter{}, nil},
		{"valid", query.TraceFilter{
			From: t0, To: t0.Add(time.Hour), MinCostNanos: &zero,
			Span: query.SpanFilter{Kind: trace.KindLLM, Status: trace.StatusError, MinDurationNs: 5},
		}, nil},
		{"reversed range", query.TraceFilter{From: t0, To: t0}, []error{query.ErrInvalidRange}},
		{"negative cost", query.TraceFilter{MinCostNanos: &neg}, []error{query.ErrInvalidFilter}},
		{
			"negative duration",
			query.TraceFilter{Span: query.SpanFilter{MinDurationNs: -1}},
			[]error{query.ErrInvalidFilter},
		},
		{"bad kind", query.TraceFilter{Span: query.SpanFilter{Kind: "x"}}, []error{query.ErrInvalidFilter}},
		{"bad status", query.TraceFilter{Span: query.SpanFilter{Status: "x"}}, []error{query.ErrInvalidFilter}},
		{
			"many",
			query.TraceFilter{
				From: t0, To: t0, MinCostNanos: &neg,
				Span: query.SpanFilter{Kind: "x", Status: "y", MinDurationNs: -1},
			},
			[]error{query.ErrInvalidRange, query.ErrInvalidFilter},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.f.Validate()
			if len(tc.wantErr) == 0 {
				require.NoError(t, err)
				return
			}
			for _, w := range tc.wantErr {
				require.ErrorIs(t, err, w)
			}
		})
	}
	bad := query.TraceFilter{MinCostNanos: &neg, Span: query.SpanFilter{Kind: "x", Status: "y", MinDurationNs: -1}}
	err := bad.Validate()
	assert.Len(t, splitLines(err.Error()), 4)
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
