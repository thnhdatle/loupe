// SPDX-License-Identifier: Apache-2.0

package query_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestNewSpanView(t *testing.T) {
	t.Parallel()
	body := []byte("hi")
	s := &trace.Span{
		SpanID: trace.SpanID{1}, ParentSpanID: trace.SpanID{2}, Name: "chat", Kind: trace.KindLLM,
		StartNs: 10, EndNs: 40, Cost: trace.Priced(7), Attributes: []byte(`{"a":1}`),
		Flags: trace.FlagUsageDuplicate | trace.FlagAttrTruncated, ContentStripped: true,
		Content: []trace.ContentPart{
			{Role: "user", Bytes: 2, Body: body, Source: trace.SourceEvent},
			{Role: "assistant", Bytes: 9, Source: trace.SourceAttribute},
		},
	}
	v := query.NewSpanView(s, true)
	assert.Equal(t, "0100000000000000", v.SpanID)
	assert.Equal(t, "0200000000000000", v.ParentSpanID)
	assert.Equal(t, int64(30), v.DurationNs)
	require.NotNil(t, v.CostNanos)
	assert.Equal(t, int64(7), *v.CostNanos)
	assert.True(t, v.UsageDuplicate)
	assert.True(t, v.AttrTruncated)
	assert.False(t, v.UsageClamped)
	assert.True(t, v.ContentStripped)
	assert.JSONEq(t, `{"a":1}`, string(v.Attributes))
	require.Len(t, v.Content, 2)
	require.NotNil(t, v.Content[0].Body)
	assert.Equal(t, "hi", *v.Content[0].Body)
	assert.Nil(t, v.Content[1].Body)

	v = query.NewSpanView(s, false)
	assert.Nil(t, v.Content[0].Body)
	assert.Equal(t, 2, v.Content[0].Bytes)
}

func TestNewSpanView_RootUnpriced(t *testing.T) {
	t.Parallel()
	v := query.NewSpanView(&trace.Span{Name: "root"}, false)
	assert.Empty(t, v.ParentSpanID)
	assert.Nil(t, v.CostNanos)
	assert.Empty(t, v.Content)
}
