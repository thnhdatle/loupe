// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var _ admin.Backuper = (*sqlite.Store)(nil)

func gptCalculator(t *testing.T) *cost.Calculator {
	t.Helper()
	table, err := cost.NewPriceTable([]cost.Price{{
		Match: cost.MatchExact, Model: "gpt-x", InputPerMtok: 1_000_000, OutputPerMtok: 2_000_000,
		EffectiveFrom: anchor.Add(-time.Hour), AsOf: anchor, Source: "test",
	}}, nil)
	require.NoError(t, err)
	calc, err := cost.NewCalculator(table)
	require.NoError(t, err)
	return calc
}

func unpricedTriage(tr int) []trace.Span {
	spans := triageTrace(tr)
	spans[3].Cost, spans[4].Cost = trace.Cost{}, trace.Cost{}
	return spans
}

func TestAdmin_RecomputeCosts_EqualsPricingAtIngest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ref := openStore(t)
	want := triageTrace(1)
	want[3].Cost, want[4].Cost = trace.Priced(0), trace.Priced(200)
	writeSpans(t, ref, want...)
	writeSpans(t, ref, mkSpan(2, 1, 0, trace.KindLLM, model("gpt-x"), usage(10, 10), at(2*time.Hour)))

	s := openStore(t)
	writeSpans(t, s, unpricedTriage(1)...)
	writeSpans(t, s, mkSpan(2, 1, 0, trace.KindLLM, model("gpt-x"), usage(10, 10), at(2*time.Hour)))
	before, err := s.GetTrace(ctx, tid(1), false)
	require.NoError(t, err)
	assert.Equal(t, 1, before.Summary.UnpricedSpans)

	calc := gptCalculator(t)
	r := admin.Range{Since: anchor, Until: anchor.Add(time.Hour)}
	after, total := int64(0), 0
	for {
		next, n, rerr := s.Admin().RecomputeCosts(ctx, calc, r, after, 2)
		require.NoError(t, rerr)
		total += n
		after = next
		if n < 2 {
			break
		}
	}
	assert.Equal(t, 5, total, "only spans starting in the range are examined")
	assert.Equal(t, snapshot(t, ref.Path()), snapshot(t, s.Path()))

	_, _, err = s.Admin().RecomputeCosts(ctx, nil, r, 0, 1)
	require.ErrorIs(t, err, sqlite.ErrInvalidOption)
}

func TestAdmin_DeleteUserData(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	writeSpans(t, s,
		mkSpan(1, 1, 0, trace.KindAgent, named("victim-marker-a"), user("victim"), session("sv")),
		mkSpan(1, 2, 1, trace.KindLLM, withContent("secret")))
	writeSpans(t, s,
		mkSpan(2, 1, 0, trace.KindAgent, user("other")),
		mkSpan(2, 2, 1, trace.KindLLM, named("victim-marker-b"), user("victim")))
	writeSpans(t, s, mkSpan(3, 1, 0, trace.KindAgent, user("other"), session("so")))

	total := 0
	for range 20 {
		n, err := s.Admin().DeleteUserData(ctx, "victim", 2)
		require.NoError(t, err)
		if n == 0 {
			break
		}
		total += n
	}
	assert.Positive(t, total)
	db := rawDB(t, s.Path())
	for _, q := range []string{
		"SELECT count(*) FROM spans WHERE user_id = 'victim' OR rollup_user = 'victim' OR name LIKE 'victim%'",
		"SELECT count(*) FROM traces WHERE user_id = 'victim'",
		"SELECT count(*) FROM sessions WHERE user_id = 'victim'",
		"SELECT count(*) FROM rollup_user_hour WHERE user_id = 'victim'",
		"SELECT count(*) FROM rollup_user_day WHERE user_id = 'victim'",
		"SELECT count(*) FROM span_content",
	} {
		assert.Zero(t, queryInt(t, db, q), q)
	}
	_, err := s.GetTrace(ctx, tid(3), false)
	require.NoError(t, err, "other users' traces stay")
	assert.Equal(t, int64(1), queryInt(t, db, "SELECT count(*) FROM traces"))
	assert.Positive(t, queryInt(t, db, "SELECT count(*) FROM rollup_hour"), "hourly rollups keep their aggregates")

	_, err = s.Admin().DeleteUserData(ctx, "", 1)
	require.ErrorIs(t, err, sqlite.ErrInvalidOption)
}

func TestStore_BackupTo(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := context.Background()
	writeSpans(t, s, simpleTrace(1)...)
	dst := filepath.Join(t.TempDir(), "backup.db")
	require.NoError(t, s.BackupTo(ctx, dst))
	require.ErrorIs(t, s.BackupTo(ctx, dst), sqlite.ErrBackupExists)
	require.ErrorIs(t, s.BackupTo(ctx, ""), sqlite.ErrInvalidOption)

	db := rawDB(t, dst)
	assert.Equal(t, int64(4), queryInt(t, db, "SELECT count(*) FROM spans"))
	assert.Equal(t, int64(2), queryInt(t, db, "PRAGMA auto_vacuum"), "the copy keeps incremental auto_vacuum")
	_, err := os.Stat(dst + "-wal")
	assert.ErrorIs(t, err, os.ErrNotExist)
}
