// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestStore_Sessions_ListAndDetail(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	// Session "a" has traces 1..5 (one per minute); "b" has trace 6 at minute 30;
	// trace 7 has no session.
	for n := 1; n <= 5; n++ {
		writeSpans(t, s, mkSpan(n, 1, 0, trace.KindLLM, at(time.Duration(n)*time.Minute), session("a"), user("ua"),
			usage(2, 1), priced(10)))
	}
	writeSpans(t, s, mkSpan(6, 1, 0, trace.KindLLM, at(30*time.Minute), session("b"), usage(1, 1)))
	writeSpans(t, s, mkSpan(7, 1, 0, trace.KindLLM, at(40*time.Minute)))

	all, err := s.ListSessions(ctx, query.SessionFilter{}, query.Page{})
	require.NoError(t, err)
	require.Len(t, all.Data, 2)
	assert.Equal(t, "b", all.Data[0].ID, "most recent activity first")
	a := all.Data[1]
	assert.Equal(t, query.SessionSummary{
		ID: "a", UserID: "ua", FirstNs: anchor.Add(time.Minute).UnixNano(),
		LastNs:     anchor.Add(5*time.Minute + 100*time.Millisecond).UnixNano(),
		TraceCount: 5, Usage: trace.Usage{Input: 10, Output: 5}, CostNanos: 50,
	}, a)

	byUser, err := s.ListSessions(ctx, query.SessionFilter{UserID: "ua"}, query.Page{})
	require.NoError(t, err)
	require.Len(t, byUser.Data, 1)
	ranged, err := s.ListSessions(ctx, query.SessionFilter{
		From: anchor.Add(20 * time.Minute),
		To:   anchor.Add(time.Hour),
	}, query.Page{})
	require.NoError(t, err)
	require.Len(t, ranged.Data, 1)
	assert.Equal(t, "b", ranged.Data[0].ID)

	first, err := s.ListSessions(ctx, query.SessionFilter{}, query.Page{Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, first.NextCursor)
	second, err := s.ListSessions(ctx, query.SessionFilter{}, query.Page{Limit: 1, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Data, 1)
	assert.Equal(t, "a", second.Data[0].ID)
	assert.Empty(t, second.NextCursor)

	var got []string
	cursor := ""
	for range 5 {
		d, err := s.GetSession(ctx, "a", query.Page{Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		assert.Equal(t, 5, d.Summary.TraceCount)
		for _, tr := range d.Data {
			got = append(got, tr.TraceID)
		}
		if cursor = d.NextCursor; cursor == "" {
			break
		}
	}
	assert.Equal(t, tids(1, 2, 3, 4, 5), got, "a session's traces by start ascending")
}

func TestStore_Sessions_Errors(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	_, err := s.GetSession(ctx, "missing", query.Page{})
	require.ErrorIs(t, err, trace.ErrNotFound)
	_, err = s.GetSession(ctx, "a", query.Page{Limit: -1})
	require.ErrorIs(t, err, query.ErrInvalidLimit)
	_, err = s.GetSession(ctx, "a", query.Page{Cursor: "!!"})
	require.ErrorIs(t, err, query.ErrInvalidCursor)
	desc := query.EncodeCursor(query.Cursor{ID: "x"}, query.SortStart, query.OrderAsc)
	_, err = s.GetSession(ctx, "a", query.Page{Cursor: desc})
	require.ErrorIs(t, err, query.ErrInvalidCursor)
	_, err = s.ListSessions(ctx, query.SessionFilter{}, query.Page{Cursor: desc})
	require.ErrorIs(t, err, query.ErrInvalidCursor)
	_, err = s.ListSessions(ctx, query.SessionFilter{}, query.Page{Limit: 1000})
	require.ErrorIs(t, err, query.ErrInvalidLimit)
	_, err = s.ListSessions(ctx, query.SessionFilter{From: anchor, To: anchor}, query.Page{})
	require.ErrorIs(t, err, query.ErrInvalidRange)
}

func TestStore_Sessions_TraceJoinsLater(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	// The first span carries no session; the trace joins "late" with its totals.
	writeSpans(t, s, mkSpan(1, 2, 1, trace.KindLLM, usage(5, 5), priced(7)))
	writeSpans(t, s, mkSpan(1, 1, 0, trace.KindAgent, session("late"), user("u")))
	d, err := s.GetSession(ctx, "late", query.Page{})
	require.NoError(t, err)
	assert.Equal(t, 1, d.Summary.TraceCount)
	assert.Equal(t, int64(7), d.Summary.CostNanos)
	assert.Equal(t, trace.Usage{Input: 5, Output: 5}, d.Summary.Usage)
	assert.Equal(t, "u", d.Summary.UserID)
	require.Len(t, d.Data, 1)
}
