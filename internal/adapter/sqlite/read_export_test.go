// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func collect(t *testing.T, s interface {
	ExportTraces(context.Context, query.ExportRequest, func([]query.TraceDetail) error) error
}, r query.ExportRequest,
) (all []query.TraceDetail, batches int) {
	t.Helper()
	err := s.ExportTraces(context.Background(), r, func(b []query.TraceDetail) error {
		batches++
		all = append(all, b...)
		return nil
	})
	require.NoError(t, err)
	return all, batches
}

func TestStore_ExportTraces_RangeAndOrder(t *testing.T) {
	t.Parallel()
	s := listFixture(t)
	all, batches := collect(t, s, query.ExportRequest{From: anchor, To: anchor.Add(time.Hour)})
	assert.Equal(t, 1, batches)
	got := make([]string, 0, len(all))
	for _, d := range all {
		got = append(got, d.Summary.TraceID)
	}
	assert.Equal(t, tids(1, 2, 3, 4, 5, 6), got)
	assert.Len(t, all[0].Spans, 3)
	assert.Len(t, all[1].Spans, 2)
	assert.Len(t, all[5].Spans, 3)
	assert.Len(t, all[0].Spans, all[0].Summary.SpanCount)

	sub, _ := collect(t, s, query.ExportRequest{From: anchor.Add(2 * time.Minute), To: anchor.Add(4 * time.Minute)})
	require.Len(t, sub, 2, "to is exclusive")
	assert.Equal(t, tid(2).String(), sub[0].Summary.TraceID)

	none, batches := collect(t, s, query.ExportRequest{From: anchor.Add(time.Hour), To: anchor.Add(2 * time.Hour)})
	assert.Empty(t, none)
	assert.Zero(t, batches)
}

func TestStore_ExportTraces_Pages(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	const n = 250
	for i := 1; i <= n; i++ {
		start := time.Duration(i/5) * time.Second
		writeSpans(t, s, mkSpan(i, 1, 0, trace.KindChain, at(start), lasting(time.Millisecond)))
	}
	all, batches := collect(t, s, query.ExportRequest{From: anchor, To: anchor.Add(time.Hour)})
	assert.Equal(t, 3, batches)
	require.Len(t, all, n)
	seen := map[string]bool{}
	for i, d := range all {
		assert.False(t, seen[d.Summary.TraceID], "duplicate trace")
		seen[d.Summary.TraceID] = true
		if i > 0 {
			assert.LessOrEqual(t, all[i-1].Summary.StartNs, d.Summary.StartNs)
		}
	}
}

func TestStore_ExportTraces_StopsOnCallbackError(t *testing.T) {
	t.Parallel()
	s := listFixture(t)
	boom := errors.New("boom")
	calls := 0
	err := s.ExportTraces(context.Background(), query.ExportRequest{From: anchor, To: anchor.Add(time.Hour)},
		func([]query.TraceDetail) error { calls++; return boom })
	require.ErrorIs(t, err, boom)
	assert.Equal(t, 1, calls)
}

func TestStore_ExportTraces_NoReadTxnDuringCallback(t *testing.T) {
	t.Parallel()
	s := listFixture(t)
	err := s.ExportTraces(context.Background(), query.ExportRequest{From: anchor, To: anchor.Add(time.Hour)},
		func([]query.TraceDetail) error {
			assert.Zero(t, s.ReadPool().Stats().InUse)
			return nil
		})
	require.NoError(t, err)
}
