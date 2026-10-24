// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestSpanMeasures(t *testing.T) {
	t.Parallel()
	usage := trace.Usage{Input: 10, Output: 5, CacheRead: 2, CacheWrite: 1, Reasoning: 3}
	cases := []struct {
		name string
		span trace.Span
		want rollup.Measures
	}{
		{
			name: "priced ok",
			span: trace.Span{Status: trace.StatusOK, Usage: usage, HasUsage: true, Cost: trace.Priced(42)},
			want: rollup.Measures{Count: 1, Usage: usage, CostNanos: 42},
		},
		{
			name: "unpriced error",
			span: trace.Span{Status: trace.StatusError, Usage: usage, HasUsage: true},
			want: rollup.Measures{Count: 1, Errors: 1, Usage: usage, Unpriced: 1},
		},
		{
			name: "no usage is not unpriced",
			span: trace.Span{Status: trace.StatusUnset},
			want: rollup.Measures{Count: 1},
		},
		{
			name: "priced zero",
			span: trace.Span{HasUsage: true, Cost: trace.Priced(0)},
			want: rollup.Measures{Count: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, rollup.SpanMeasures(&tc.span))
		})
	}
}

func TestAggregate_AddSpan_DuplicateOnlyInHistogram(t *testing.T) {
	t.Parallel()
	kept := trace.Span{StartNs: 0, EndNs: 2e6, Usage: trace.Usage{Input: 7}, HasUsage: true, Cost: trace.Priced(9)}
	dup := kept
	dup.EndNs = 3e6
	dup.Flags = trace.FlagUsageDuplicate | trace.FlagUsageClamped
	var a rollup.Aggregate
	a.AddSpan(&kept)
	a.AddSpan(&dup)
	assert.Equal(t, rollup.Measures{Count: 1, Usage: trace.Usage{Input: 7}, CostNanos: 9}, a.Measures)
	assert.Equal(t, uint64(2), a.Histogram.Count())
}

func TestAggregate_Apply_NegativeDelta(t *testing.T) {
	t.Parallel()
	s := trace.Span{EndNs: 1e6, Usage: trace.Usage{Input: 7, Output: 3}, HasUsage: true, Cost: trace.Priced(9)}
	var a rollup.Aggregate
	a.AddSpan(&s)
	a.Apply(rollup.Measures{}.Sub(rollup.SpanMeasures(&s)))
	assert.True(t, a.Measures.IsZero())
	assert.Equal(t, uint64(1), a.Histogram.Count())
}

func TestAggregate_MergeAndClone(t *testing.T) {
	t.Parallel()
	s := trace.Span{EndNs: 1e6, Status: trace.StatusError, HasUsage: true}
	var a, b rollup.Aggregate
	a.AddSpan(&s)
	b.AddSpan(&s)
	b.AddSpan(&s)
	a.Merge(&b)
	assert.Equal(t, rollup.Measures{Count: 3, Errors: 3, Unpriced: 3}, a.Measures)
	assert.Equal(t, uint64(3), a.Histogram.Count())
	c := a.Clone()
	a.AddSpan(&s)
	assert.Equal(t, int64(3), c.Measures.Count)
	assert.Equal(t, uint64(3), c.Histogram.Count())
}

func measuresGen() *rapid.Generator[rollup.Measures] {
	n := rapid.Int64Range(-1e12, 1e12)
	return rapid.Custom(func(t *rapid.T) rollup.Measures {
		return rollup.Measures{
			Count: n.Draw(t, "count"), Errors: n.Draw(t, "errors"),
			Usage: trace.Usage{
				Input: n.Draw(t, "in"), Output: n.Draw(t, "out"), CacheRead: n.Draw(t, "cr"),
				CacheWrite: n.Draw(t, "cw"), Reasoning: n.Draw(t, "re"),
			},
			CostNanos: n.Draw(t, "cost"), Unpriced: n.Draw(t, "unpriced"),
		}
	})
}

func TestMeasures_AddSub_InverseProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		m, d := measuresGen().Draw(t, "m"), measuresGen().Draw(t, "d")
		require.Equal(t, m, m.Add(d).Sub(d))
		require.Equal(t, m.Add(d), d.Add(m))
		require.True(t, m.Sub(m).IsZero())
	})
}
