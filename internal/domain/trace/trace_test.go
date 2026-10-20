// SPDX-License-Identifier: Apache-2.0

package trace_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestParseTraceID_RoundTrip(t *testing.T) {
	t.Parallel()
	const s = "0102030405060708090a0b0c0d0e0f10"
	id, err := trace.ParseID(s)
	require.NoError(t, err)
	assert.Equal(t, s, id.String())
	assert.False(t, id.IsZero())
}

func TestParseTraceID_Invalid(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "01", "zz02030405060708090a0b0c0d0e0f10"} {
		_, err := trace.ParseID(s)
		require.ErrorIs(t, err, trace.ErrInvalidID, s)
	}
}

func TestParseSpanID_RoundTripAndInvalid(t *testing.T) {
	t.Parallel()
	id, err := trace.ParseSpanID("0102030405060708")
	require.NoError(t, err)
	assert.Equal(t, "0102030405060708", id.String())
	_, err = trace.ParseSpanID("01")
	require.ErrorIs(t, err, trace.ErrInvalidID)
}

func TestIDFromBytes(t *testing.T) {
	t.Parallel()
	tid, err := trace.IDFromBytes(make([]byte, 16))
	require.NoError(t, err)
	assert.True(t, tid.IsZero())
	_, err = trace.IDFromBytes(make([]byte, 3))
	require.ErrorIs(t, err, trace.ErrInvalidID)

	sid, err := trace.SpanIDFromBytes(nil)
	require.NoError(t, err)
	assert.True(t, sid.IsZero())
	sid, err = trace.SpanIDFromBytes([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	require.NoError(t, err)
	assert.Equal(t, "0102030405060708", sid.String())
	_, err = trace.SpanIDFromBytes([]byte{1})
	require.ErrorIs(t, err, trace.ErrInvalidID)
}

func TestKind_IsValidAndDedupe(t *testing.T) {
	t.Parallel()
	for _, k := range trace.Kinds() {
		assert.True(t, k.IsValid(), k)
		assert.Equal(t, k == trace.KindLLM || k == trace.KindEmbedding, k.HasUsageDedupe(), k)
	}
	assert.False(t, trace.Kind("span").IsValid())
}

func TestStatusFromCode(t *testing.T) {
	t.Parallel()
	assert.Equal(t, trace.StatusUnset, trace.StatusFromCode(trace.StatusCodeUnset))
	assert.Equal(t, trace.StatusOK, trace.StatusFromCode(trace.StatusCodeOK))
	assert.Equal(t, trace.StatusError, trace.StatusFromCode(trace.StatusCodeError))
	assert.Equal(t, trace.StatusUnset, trace.StatusFromCode(7))
}

func TestFlags_Has(t *testing.T) {
	t.Parallel()
	f := trace.FlagAttrTruncated | trace.FlagUsageClamped
	assert.True(t, f.Has(trace.FlagAttrTruncated))
	assert.True(t, f.Has(trace.FlagUsageClamped))
	assert.False(t, f.Has(trace.FlagUsageDuplicate))
	assert.False(t, f.Has(trace.FlagUsageDuplicate|trace.FlagUsageClamped))
}

func TestUsage_AddSubIsZero(t *testing.T) {
	t.Parallel()
	a := trace.Usage{Input: 10, Output: 5, CacheRead: 2, CacheWrite: 1, Reasoning: 3}
	b := trace.Usage{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1, Reasoning: 1}
	assert.Equal(t, a, a.Add(b).Sub(b))
	assert.True(t, a.Sub(a).IsZero())
	assert.False(t, a.IsZero())
}

func TestSpan_DurationAndRoot(t *testing.T) {
	t.Parallel()
	s := trace.Span{StartNs: 10, EndNs: 25}
	assert.Equal(t, int64(15), s.DurationNs())
	assert.True(t, s.IsRoot())
	s.EndNs = 5
	assert.Equal(t, int64(0), s.DurationNs())
	s.ParentSpanID = trace.SpanID{1}
	assert.False(t, s.IsRoot())
}

func TestValueConstructors(t *testing.T) {
	t.Parallel()
	assert.Equal(t, trace.ValueString, trace.StringValue("x").Type)
	assert.Equal(t, int64(3), trace.IntValue(3).Int)
	assert.InDelta(t, 1.5, trace.DoubleValue(1.5).Double, 0)
	assert.True(t, trace.BoolValue(true).Bool)
	assert.Len(t, trace.ArrayValue(trace.IntValue(1), trace.IntValue(2)).Array, 2)
	assert.Equal(t, "k", trace.MapValue(trace.Attr{Key: "k"}).Map[0].Key)
	assert.Equal(t, trace.Cost{Nanos: 4, Priced: true}, trace.Priced(4))
	p := trace.ContentPart{}
	assert.True(t, p.IsMarker())
}
