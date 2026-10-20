// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var _ retention.Store = (*sqlite.Store)(nil)

func withContent(body string) spanOpt {
	return func(s *trace.Span) {
		s.Content = append(s.Content, trace.ContentPart{
			Source: trace.SourceAttribute, Key: "input.value",
			Part: len(s.Content), Bytes: len(body), Body: []byte(body),
		})
	}
}

func TestStore_Purge_ContentOldestFirst(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		writeSpans(t, s, mkSpan(i, 1, 0, trace.KindLLM, at(time.Duration(i)*time.Hour), withContent("x")))
	}
	db := rawDB(t, s.Path())
	n, err := s.Purge(ctx, retention.ClassContent, anchor.Add(4*time.Hour), 2)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, int64(3), queryInt(t, db, "SELECT count(*) FROM span_content"))
	assert.Equal(t, anchor.Add(3*time.Hour).UnixNano(), queryInt(t, db, "SELECT MIN(time_ns) FROM span_content"),
		"the two oldest went first")
	n, err = s.Purge(ctx, retention.ClassContent, anchor.Add(4*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "a count below limit means none is left")
	assert.Equal(t, int64(5), queryInt(t, db, "SELECT count(*) FROM spans"), "spans are untouched")
}

func TestStore_Purge_OrphanContentByInsertTime(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	old := trace.ContentPart{
		TraceID: tid(1), SpanID: sid(5), Source: trace.SourceLog, Key: "gen_ai.choice",
		Bytes: 1, Body: []byte("a"), TimeNs: anchor.Add(-48 * time.Hour).UnixNano(),
	}
	attached := old
	attached.SpanID = sid(1)
	_, err := s.WriteBatch(ctx, &trace.Batch{Content: []trace.ContentPart{old, attached}})
	require.NoError(t, err)
	writeSpans(t, s, mkSpan(1, 1, 0, trace.KindLLM))

	// Inserted at the anchor (store clock): not yet older than the cutoff,
	// although the event time is two days old.
	n, err := s.Purge(ctx, retention.ClassOrphanContent, anchor, 10)
	require.NoError(t, err)
	assert.Zero(t, n)
	n, err = s.Purge(ctx, retention.ClassOrphanContent, anchor.Add(time.Second), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	db := rawDB(t, s.Path())
	assert.Equal(t, int64(1), queryInt(t, db, "SELECT count(*) FROM span_content WHERE has_span = 1"))
}

func TestStore_Purge_SpansWithContentAndEmptyTraces(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	writeSpans(t, s, mkSpan(1, 1, 0, trace.KindLLM, at(0), withContent("gone"), usage(1, 1), priced(5)))
	writeSpans(t, s,
		mkSpan(2, 1, 0, trace.KindAgent, at(time.Hour), usage(2, 2), priced(5)),
		mkSpan(2, 2, 1, trace.KindLLM, at(3*time.Hour), usage(3, 3), priced(7)))
	n, err := s.Purge(ctx, retention.ClassSpans, anchor.Add(2*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	_, err = s.GetTrace(ctx, tid(1), false)
	require.ErrorIs(t, err, trace.ErrNotFound, "a trace without spans is deleted")
	d, err := s.GetTrace(ctx, tid(2), false)
	require.NoError(t, err)
	assert.Len(t, d.Spans, 1)
	assert.Equal(t, int64(12), d.Summary.CostNanos, "totals of a straddling trace are not adjusted")
	db := rawDB(t, s.Path())
	assert.Equal(t, int64(0), queryInt(t, db, "SELECT count(*) FROM span_content"))
}

func TestStore_Purge_SessionsWithoutTraces(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	writeSpans(t, s, mkSpan(1, 1, 0, trace.KindLLM, session("a")))
	writeSpans(t, s, mkSpan(2, 1, 0, trace.KindLLM, session("b"), at(time.Hour)))
	_, err := s.Purge(ctx, retention.ClassSpans, anchor.Add(30*time.Minute), 10)
	require.NoError(t, err)
	n, err := s.Purge(ctx, retention.ClassSessions, anchor.Add(24*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = s.GetSession(ctx, "a", query.Page{})
	require.ErrorIs(t, err, trace.ErrNotFound)
	_, err = s.GetSession(ctx, "b", query.Page{})
	require.NoError(t, err)
}

func TestStore_Purge_RollupsAndIngestStats(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	for h := range 3 {
		writeSpans(t, s, mkSpan(h+1, 1, 0, trace.KindLLM, at(time.Duration(h)*25*time.Hour), user("u")))
	}
	_, err := s.WriteBatch(ctx, &trace.Batch{Counts: []trace.IngestCount{
		{AtNs: anchor.UnixNano(), Signal: trace.SignalTraces, Result: trace.ResultReceived, N: 1},
	}})
	require.NoError(t, err)
	db := rawDB(t, s.Path())

	// Hour rows at 0h, 25h, 50h: whole hours before 26h are 0h and 25h.
	n, err := s.Purge(ctx, retention.ClassRollupsHourly, anchor.Add(26*time.Hour), 3)
	require.NoError(t, err)
	assert.Equal(t, 3, n, "the limit is shared by rollup_hour and rollup_user_hour")
	n, err = s.Purge(ctx, retention.ClassRollupsHourly, anchor.Add(26*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(1), queryInt(t, db, "SELECT count(*) FROM rollup_hour"))
	assert.Equal(t, int64(1), queryInt(t, db, "SELECT count(*) FROM rollup_user_hour"))

	// Day rows at day 0, 1, 2; a cutoff inside day 1 removes day 0 only, plus
	// the ingest_stats hours before it (received and stored at hour 0).
	n, err = s.Purge(ctx, retention.ClassRollupsDaily, anchor.Add(36*time.Hour), 10)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, int64(2), queryInt(t, db, "SELECT count(*) FROM rollup_day"))
	assert.Equal(t, int64(0), queryInt(t, db, "SELECT count(*) FROM ingest_stats WHERE result = 'received'"))
}

func TestStore_Purge_InvalidInput(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	_, err := s.Purge(context.Background(), "bogus", anchor, 1)
	require.ErrorIs(t, err, sqlite.ErrUnknownClass)
	_, err = s.Purge(context.Background(), retention.ClassSpans, anchor, 0)
	require.ErrorIs(t, err, sqlite.ErrInvalidOption)
	_, err = s.IncrementalVacuum(context.Background(), 0)
	require.ErrorIs(t, err, sqlite.ErrInvalidOption)
}

func TestStore_SizeVacuumAndCheckpoint(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	body := string(bytes.Repeat([]byte{'z'}, 3000))
	for i := 1; i <= 200; i++ {
		writeSpans(t, s, mkSpan(i, 1, 0, trace.KindLLM, withContent(body+string(rune(i)))))
	}
	size, err := s.Size(ctx)
	require.NoError(t, err)
	main, err := os.Stat(s.Path())
	require.NoError(t, err)
	assert.Greater(t, size, main.Size(), "size includes the WAL and SHM")

	_, err = s.Purge(ctx, retention.ClassSpans, anchor.Add(time.Hour), 1000)
	require.NoError(t, err)
	busy, err := s.Checkpoint(ctx)
	require.NoError(t, err)
	assert.False(t, busy)
	wal, err := os.Stat(s.Path() + "-wal")
	require.NoError(t, err)
	assert.Zero(t, wal.Size(), "TRUNCATE empties the WAL")

	db := rawDB(t, s.Path())
	free := queryInt(t, db, "PRAGMA freelist_count")
	require.Positive(t, free)
	left, err := s.IncrementalVacuum(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, int(free-1), left)
	freed, err := s.Admin().IncrementalVacuum(ctx, 1_000_000)
	require.NoError(t, err)
	assert.Equal(t, left, freed)
	require.NoError(t, s.Admin().Checkpoint(ctx))

	disk, err := s.FreeDisk(ctx)
	require.NoError(t, err)
	assert.Positive(t, disk)
}
