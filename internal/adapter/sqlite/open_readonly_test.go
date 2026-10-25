// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestOpenReadOnly_ReadsWhileWriterHoldsLock(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	w := openAt(t, path)
	writeSpans(t, w, mkSpan(1, 1, 0, trace.KindLLM))

	ro, err := sqlite.OpenReadOnly(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ro.Close() })

	var n int
	err = ro.ExportTraces(context.Background(), query.ExportRequest{From: anchor.Add(-1), To: anchor.Add(3600e9)},
		func(b []query.TraceDetail) error { n += len(b); return nil })
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestOpenReadOnly_WritesFailWithoutPanic(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	openAt(t, path)
	ro, err := sqlite.OpenReadOnly(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ro.Close() })

	_, err = ro.WriteBatch(context.Background(), &trace.Batch{Spans: []trace.Span{mkSpan(1, 1, 0, trace.KindLLM)}})
	require.Error(t, err)
	require.Error(t, ro.FullVacuum(context.Background()))
	_, err = ro.Checkpoint(context.Background())
	require.Error(t, err)
	require.NoError(t, ro.Close())
}

func TestOpenReadOnly_RejectsPendingMigrations(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	s, err := sqlite.Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = sqlite.OpenReadOnly(context.Background(), path, sqlite.WithMigrationsFS(nextMigrations(t)))
	require.ErrorIs(t, err, sqlite.ErrPendingMigrations)
}

func TestOpenReadOnly_MissingDatabase(t *testing.T) {
	t.Parallel()
	_, err := sqlite.OpenReadOnly(context.Background(), dbPath(t))
	require.Error(t, err)
}
