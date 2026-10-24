// SPDX-License-Identifier: Apache-2.0

package retention_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/retention"
)

const mib = int64(1) << 20

func capEnv(t *testing.T) *env {
	t.Helper()
	cfg := testConfig()
	cfg.MaxDBBytes = 64 * mib
	return newEnv(t, cfg)
}

func spanID(i int) string { return fmt.Sprintf("s%02d", i) }

func seedHourly(s *fakeStore, n, withContent int) {
	for i := range n {
		at := ago(time.Duration(i+2) * time.Hour)
		s.addSpan(spanID(i), "", at, mib)
		if i < withContent {
			s.addContent(spanID(i), at, mib)
		}
	}
}

func TestPlanner_Cap_DeletesOldestContentFirst(t *testing.T) {
	t.Parallel()
	e := capEnv(t)
	seedHourly(e.store, 40, 40)
	require.NoError(t, e.p.Pass(context.Background()))

	assert.Len(t, e.store.spanIDs(), 40)
	wantContent := make([]string, 0, 22)
	for i := range 22 {
		wantContent = append(wantContent, spanID(i))
	}
	assert.Equal(t, wantContent, e.store.contentSpans(), "the 18 oldest content rows went")
	st := e.p.Stats()
	assert.Equal(t, map[retention.Class]int64{retention.ClassContent: 18, retention.ClassSpans: 0}, st.CapDeleted)
	assert.Equal(t, int64(1), st.CapExceeded)
	assert.Zero(t, st.CapUnmet)
	assert.Equal(t, 62*mib, st.DBSizeBytes)
	assert.Equal(t, 62*mib, e.store.sizeNow())
	assert.Zero(t, st.Deleted[retention.ClassContent], "cap deletions are counted apart from retention")
	assert.Contains(t, e.logs.String(), "level=WARN msg=\"database above retention.max_db_bytes; deleted oldest data\"")
	assert.Equal(t, 6, count(e.runner.jobNames(), "retention_disk_cap_content"))
	assert.Equal(t, 7, count(e.runner.jobNames(), "retention_checkpoint"), "after the pass and after every cap job")
}

func TestPlanner_Cap_DeletesOldestSpansWhenContentIsGone(t *testing.T) {
	t.Parallel()
	e := capEnv(t)
	seedHourly(e.store, 70, 5)
	require.NoError(t, e.p.Pass(context.Background()))

	assert.Empty(t, e.store.contentSpans())
	wantSpans := make([]string, 0, 64)
	for i := range 64 {
		wantSpans = append(wantSpans, spanID(i))
	}
	assert.Equal(t, wantSpans, e.store.spanIDs(), "the six oldest spans went")
	st := e.p.Stats()
	assert.Equal(t, map[retention.Class]int64{retention.ClassContent: 5, retention.ClassSpans: 6}, st.CapDeleted)
	assert.Equal(t, 64*mib, st.DBSizeBytes)

	capCalls := e.store.purgeCalls()[len(retention.Classes()):]
	want := []purgeCall{
		{class: retention.ClassContent, cutoff: ago(time.Hour), limit: 3, n: 3},
		{class: retention.ClassContent, cutoff: ago(time.Hour), limit: 3, n: 2},
		{class: retention.ClassContent, cutoff: ago(time.Hour), limit: 3, n: 0},
		{class: retention.ClassSpans, cutoff: ago(time.Hour), limit: 3, n: 3},
		{class: retention.ClassSpans, cutoff: ago(time.Hour), limit: 3, n: 3},
	}
	assert.Equal(t, want, capCalls, "content is not retried once it is gone")
}

func TestPlanner_Cap_NeverDeletesTheLastHour(t *testing.T) {
	t.Parallel()
	e := capEnv(t)
	for i := range 10 {
		e.store.addSpan(fmt.Sprintf("old%d", i), "", ago(time.Duration(i+2)*time.Hour), mib)
	}
	for i := range 66 {
		at := ago(time.Duration(i+1) * 50 * time.Second)
		e.store.addSpan(fmt.Sprintf("new%02d", i), "", at, mib)
		e.store.addContent(fmt.Sprintf("new%02d", i), at, 1)
	}
	require.NoError(t, e.p.Pass(context.Background()))

	ids := e.store.spanIDs()
	assert.Len(t, ids, 66)
	for _, id := range ids {
		assert.Contains(t, id, "new")
	}
	assert.Len(t, e.store.contentSpans(), 66, "recent content survives too")
	st := e.p.Stats()
	assert.Equal(t, int64(10), st.CapDeleted[retention.ClassSpans])
	assert.Equal(t, int64(1), st.CapUnmet)
	assert.Greater(t, st.DBSizeBytes, 64*mib)
	assert.Contains(t, e.logs.String(), "level=ERROR msg=\"database above retention.max_db_bytes with only the last hour")
	assert.Zero(t, st.PassErrors, "an unmet cap is not a pass error; ingest continues")
}

func TestPlanner_Cap_BusyCheckpointEndsTheCapPhase(t *testing.T) {
	t.Parallel()
	e := capEnv(t)
	seedHourly(e.store, 40, 40)
	e.store.set(func(s *fakeStore) { s.busy = true })
	require.NoError(t, e.p.Pass(context.Background()))
	st := e.p.Stats()
	assert.Equal(t, int64(3), st.CapDeleted[retention.ClassContent], "one job, then wait for the next pass")
	assert.Equal(t, int64(2), st.CheckpointBusy)
	assert.Zero(t, st.CapUnmet)
}

func TestPlanner_Cap_UnderCapDeletesNothing(t *testing.T) {
	t.Parallel()
	e := capEnv(t)
	seedHourly(e.store, 10, 10)
	require.NoError(t, e.p.Pass(context.Background()))
	st := e.p.Stats()
	assert.Zero(t, st.CapExceeded)
	assert.Equal(t, 20*mib, st.DBSizeBytes)
	assert.Len(t, e.store.contentSpans(), 10)
	assert.NotContains(t, e.logs.String(), "max_db_bytes")
}

func TestPlanner_Cap_OffMeasuresButNeverDeletes(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	seedHourly(e.store, 100, 100)
	require.NoError(t, e.p.Pass(context.Background()))
	sizes, _ := e.store.counts()
	assert.Equal(t, 1, sizes, "one measurement per pass, for loupe_db_size_bytes")
	assert.Len(t, e.store.spanIDs(), 100)
}

func TestPlanner_Cap_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		inject func(s *fakeStore)
		seed   int
	}{
		{name: "size", inject: func(s *fakeStore) { s.sizeErr = errBoom }, seed: 40},
		{
			name: "content purge",
			inject: func(s *fakeStore) {
				s.onPurge = func(retention.Class) {
					if len(s.purges) >= len(retention.Classes()) {
						s.purgeErr[retention.ClassContent] = errBoom
					}
				}
			},
			seed: 40,
		},
		{
			name: "spans purge",
			inject: func(s *fakeStore) {
				s.onPurge = func(retention.Class) {
					if len(s.purges) >= len(retention.Classes()) {
						s.purgeErr[retention.ClassSpans] = errBoom
					}
				}
			},
			seed: 0,
		},
		{
			name: "compact after a cap job",
			inject: func(s *fakeStore) {
				s.onPurge = func(retention.Class) {
					if len(s.purges) >= len(retention.Classes()) {
						s.vacuumErr = errBoom
					}
				}
			},
			seed: 40,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := capEnv(t)
			seedHourly(e.store, 70, tc.seed)
			e.store.set(tc.inject)
			require.ErrorIs(t, e.p.Pass(context.Background()), errBoom)
			st := e.p.Stats()
			assert.Equal(t, int64(1), st.PassErrors)
			assert.LessOrEqual(t, st.CapDeleted[retention.ClassContent]+st.CapDeleted[retention.ClassSpans], int64(3),
				"the cap phase stops at the first error")
		})
	}
}

func TestPlanner_Pass_MeasuresSizeWithoutCap(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.MaxDBBytes = 0
	e := newEnv(t, cfg)
	e.store.addSpan("a", "", ago(2*time.Hour), mib)
	require.NoError(t, e.p.Pass(context.Background()))
	assert.Equal(t, e.store.sizeNow(), e.p.Stats().DBSizeBytes)
	assert.Positive(t, e.p.Stats().DBSizeBytes)
}
