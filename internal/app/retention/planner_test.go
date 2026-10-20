// SPDX-License-Identifier: Apache-2.0

package retention_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

var epoch = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

var errBoom = errors.New("boom")

type env struct {
	store  *fakeStore
	runner *fakeRunner
	clk    *clock.Fake
	logs   *logBuffer
	p      *retention.Planner
}

// testConfig is the default config with a small batch and the cap off.
func testConfig() retention.Config {
	cfg := retention.DefaultConfig()
	cfg.Batch = 3
	cfg.MaxDBBytes = 0
	return cfg
}

func newEnv(t *testing.T, cfg retention.Config, opts ...retention.Option) *env {
	t.Helper()
	e := &env{store: newFakeStore(), runner: &fakeRunner{}, clk: clock.NewFake(epoch), logs: &logBuffer{}}
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	opts = append([]retention.Option{retention.WithLogger(log)}, opts...)
	p, err := retention.New(e.store, e.runner, e.clk, cfg, opts...)
	require.NoError(t, err)
	e.p = p
	return e
}

func ago(d time.Duration) time.Time { return epoch.Add(-d) }

func count(names []string, name string) int {
	n := 0
	for _, s := range names {
		if s == name {
			n++
		}
	}
	return n
}

func TestNew_InvalidArguments(t *testing.T) {
	t.Parallel()
	store, runner, clk := newFakeStore(), &fakeRunner{}, clock.NewFake(epoch)
	bad := testConfig()
	bad.Batch = 0
	tests := []struct {
		name    string
		store   retention.Store
		runner  trace.JobRunner
		clk     clock.Clock
		cfg     retention.Config
		opts    []retention.Option
		wantErr error
	}{
		{name: "nil store", runner: runner, clk: clk, cfg: testConfig(), wantErr: retention.ErrInvalidArgument},
		{name: "nil runner", store: store, clk: clk, cfg: testConfig(), wantErr: retention.ErrInvalidArgument},
		{name: "nil clock", store: store, runner: runner, cfg: testConfig(), wantErr: retention.ErrInvalidArgument},
		{name: "invalid config", store: store, runner: runner, clk: clk, cfg: bad, wantErr: retention.ErrInvalidConfig},
		{
			name: "nil logger", store: store, runner: runner, clk: clk, cfg: testConfig(),
			opts: []retention.Option{retention.WithLogger(nil)}, wantErr: retention.ErrInvalidArgument,
		},
		{
			name: "zero vacuum pages", store: store, runner: runner, clk: clk, cfg: testConfig(),
			opts: []retention.Option{retention.WithVacuumPages(0)}, wantErr: retention.ErrInvalidArgument,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := retention.New(tc.store, tc.runner, tc.clk, tc.cfg, tc.opts...)
			require.ErrorIs(t, err, tc.wantErr)
			assert.Nil(t, p)
		})
	}
}

func TestPlanner_Pass_DeletesEachClassPastItsAge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		class retention.Class
		seed  func(s *fakeStore)
		check func(t *testing.T, s *fakeStore)
		want  int64
	}{
		{
			name:  "spans",
			class: retention.ClassSpans,
			seed: func(s *fakeStore) {
				s.addSpan("old", "", ago(14*day+time.Minute), pageSize)
				s.addSpan("edge", "", ago(14*day), pageSize) // exactly at the cutoff: kept
				s.addSpan("new", "", ago(14*day-time.Minute), pageSize)
			},
			check: func(t *testing.T, s *fakeStore) {
				t.Helper()
				assert.Equal(t, []string{"edge", "new"}, s.spanIDs())
			},
			want: 1,
		},
		{
			name:  "content",
			class: retention.ClassContent,
			seed: func(s *fakeStore) {
				s.addSpan("a", "", ago(8*day), pageSize)
				s.addSpan("b", "", ago(6*day), pageSize)
				s.addContent("a", ago(8*day), pageSize)
				s.addContent("b", ago(6*day), pageSize)
			},
			check: func(t *testing.T, s *fakeStore) {
				t.Helper()
				assert.Equal(t, []string{"b"}, s.contentSpans())
				assert.Equal(t, []string{"a", "b"}, s.spanIDs(), "spans outlive their content")
			},
			want: 1,
		},
		{
			name:  "orphan content",
			class: retention.ClassOrphanContent,
			seed: func(s *fakeStore) {
				s.addSpan("stored", "", ago(3*time.Hour), pageSize)
				s.addContent("stored", ago(3*time.Hour), pageSize)
				s.addContent("missing-old", ago(2*time.Hour), pageSize)
				s.addContent("missing-new", ago(30*time.Minute), pageSize)
			},
			check: func(t *testing.T, s *fakeStore) {
				t.Helper()
				assert.Equal(t, []string{"stored", "missing-new"}, s.contentSpans())
			},
			want: 1,
		},
		{
			name:  "sessions without traces",
			class: retention.ClassSessions,
			seed: func(s *fakeStore) {
				s.addSpan("a", "live", ago(time.Hour), pageSize)
				s.addSession("live", ago(time.Hour))
				s.addSession("empty", ago(time.Hour))
			},
			check: func(t *testing.T, s *fakeStore) {
				t.Helper()
				assert.Equal(t, []string{"live"}, s.sessionIDs())
			},
			want: 1,
		},
		{
			name:  "hourly rollups and user rollups",
			class: retention.ClassRollupsHourly,
			seed: func(s *fakeStore) {
				for _, tbl := range []string{"rollup_hour", "rollup_user_hour"} {
					s.addBucket(tbl, ago(35*day+time.Hour), time.Hour)      // ends at the cutoff: deleted
					s.addBucket(tbl, ago(35*day+30*time.Minute), time.Hour) // straddles: kept
				}
				s.addBucket("rollup_day", ago(36*day), day) // daily retention is longer
			},
			check: func(t *testing.T, s *fakeStore) {
				t.Helper()
				assert.Equal(t, []string{"rollup_hour", "rollup_user_hour", "rollup_day"}, s.bucketTables())
			},
			want: 2,
		},
		{
			name:  "daily rollups, user rollups and ingest stats",
			class: retention.ClassRollupsDaily,
			seed: func(s *fakeStore) {
				for _, tbl := range []string{"rollup_day", "rollup_user_day"} {
					s.addBucket(tbl, ago(401*day), day)
					s.addBucket(tbl, ago(400*day+time.Hour), day) // straddles: kept
				}
				s.addBucket("ingest_stats", ago(400*day+time.Hour), time.Hour)
				s.addBucket("ingest_stats", ago(400*day+30*time.Minute), time.Hour)
			},
			check: func(t *testing.T, s *fakeStore) {
				t.Helper()
				assert.Equal(t, []string{"rollup_day", "rollup_user_day", "ingest_stats"}, s.bucketTables())
			},
			want: 3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, testConfig())
			tc.seed(e.store)
			require.NoError(t, e.p.Pass(context.Background()))
			tc.check(t, e.store)
			st := e.p.Stats()
			for _, c := range retention.Classes() {
				if c == tc.class {
					assert.Equal(t, tc.want, st.Deleted[c], c)
				} else {
					assert.Zero(t, st.Deleted[c], c)
				}
			}
			assert.Equal(t, int64(1), st.Passes)
			assert.Zero(t, st.PassErrors)
		})
	}
}

func TestPlanner_Pass_ClassOrderCutoffsAndJobNames(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	require.NoError(t, e.p.Pass(context.Background()))
	cfg := testConfig()
	want := []purgeCall{
		{class: retention.ClassContent, cutoff: ago(cfg.Content), limit: 3},
		{class: retention.ClassOrphanContent, cutoff: ago(cfg.OrphanContent), limit: 3},
		{class: retention.ClassSpans, cutoff: ago(cfg.Spans), limit: 3},
		{class: retention.ClassSessions, cutoff: epoch, limit: 3},
		{class: retention.ClassRollupsHourly, cutoff: ago(cfg.RollupsHourly), limit: 3},
		{class: retention.ClassRollupsDaily, cutoff: ago(cfg.RollupsDaily), limit: 3},
	}
	assert.Equal(t, want, e.store.purgeCalls())
	assert.Equal(t, []string{
		"retention_content", "retention_orphan_content", "retention_spans", "retention_sessions",
		"retention_rollups_hourly", "retention_rollups_daily", "retention_vacuum", "retention_checkpoint",
	}, e.runner.jobNames())
}

func TestPlanner_Pass_BatchesUntilAJobDeletesFewer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		old      int
		wantJobs []int // rows deleted per spans job
	}{
		{name: "none", old: 0, wantJobs: []int{0}},
		{name: "partial last batch", old: 7, wantJobs: []int{3, 3, 1}},
		{name: "exact multiple needs an empty job", old: 6, wantJobs: []int{3, 3, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, testConfig())
			for i := range tc.old {
				e.store.addSpan(fmt.Sprintf("old%d", i), "", ago(20*day-time.Duration(i)*time.Minute), pageSize)
			}
			e.store.addSpan("young", "", ago(time.Hour), pageSize)
			require.NoError(t, e.p.Pass(context.Background()))
			var got []int
			for _, c := range e.store.purgeCalls() {
				if c.class == retention.ClassSpans {
					got = append(got, c.n)
				}
			}
			assert.Equal(t, tc.wantJobs, got)
			assert.Equal(t, len(tc.wantJobs), count(e.runner.jobNames(), "retention_spans"))
			assert.Equal(t, []string{"young"}, e.store.spanIDs())
			assert.Equal(t, int64(tc.old), e.p.Stats().Deleted[retention.ClassSpans])
		})
	}
}

func TestPlanner_Pass_CompactsInBoundedVacuumJobsThenCheckpoints(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig(), retention.WithVacuumPages(2))
	for i := range 5 {
		e.store.addSpan(fmt.Sprintf("s%d", i), "", ago(20*day), pageSize) // 5 pages freed
	}
	require.NoError(t, e.p.Pass(context.Background()))
	names := e.runner.jobNames()
	assert.Equal(t, 3, count(names, "retention_vacuum"), "5 free pages at 2 per job")
	assert.Equal(t, "retention_checkpoint", names[len(names)-1])
	assert.Equal(t, []string{"retention_vacuum", "retention_vacuum", "retention_vacuum"}, names[len(names)-4:len(names)-1])
	assert.Zero(t, e.store.sizeNow(), "free list and WAL released")
}

func TestPlanner_Pass_VacuumStopsWhenFreeListStopsShrinking(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	e.store.set(func(s *fakeStore) { s.stuckPages = 7 })
	require.NoError(t, e.p.Pass(context.Background()))
	_, vacuums := e.store.counts()
	assert.Equal(t, 2, vacuums)
}

func TestPlanner_Pass_CheckpointBusyIsNotAnError(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	e.store.set(func(s *fakeStore) { s.busy = true })
	require.NoError(t, e.p.Pass(context.Background()))
	assert.Equal(t, int64(1), e.p.Stats().CheckpointBusy)
	assert.Contains(t, e.logs.String(), "wal checkpoint busy")
}

func TestPlanner_Pass_StoreErrorsDoNotStopOtherSteps(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		inject  func(s *fakeStore)
		wantJob string
	}{
		{
			name:    "purge",
			inject:  func(s *fakeStore) { s.purgeErr[retention.ClassSpans] = errBoom },
			wantJob: "retention_rollups_daily",
		},
		{name: "vacuum", inject: func(s *fakeStore) { s.vacuumErr = errBoom }, wantJob: "retention_vacuum"},
		{name: "checkpoint", inject: func(s *fakeStore) { s.checkErr = errBoom }, wantJob: "retention_checkpoint"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, testConfig())
			e.store.addBucket("rollup_day", ago(500*day), day)
			e.store.set(tc.inject)
			err := e.p.Pass(context.Background())
			require.ErrorIs(t, err, errBoom)
			assert.Contains(t, e.runner.jobNames(), tc.wantJob)
			assert.Empty(t, e.store.bucketTables(), "later classes still ran")
			st := e.p.Stats()
			assert.Equal(t, int64(1), st.PassErrors)
			assert.Equal(t, int64(1), st.Passes)
		})
	}
}

func TestPlanner_Pass_WriterShuttingDown(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	e.runner.stop()
	err := e.p.Pass(context.Background())
	require.ErrorIs(t, err, trace.ErrShuttingDown)
	assert.Empty(t, e.store.purgeCalls())
	assert.Zero(t, e.p.Stats().PassErrors, "shutdown is not a pass error")
}

func TestPlanner_Pass_ShutdownDuringCompactStopsThePass(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.MaxDBBytes = retention.MinMaxDBBytes
	e := newEnv(t, cfg)
	e.runner.block = func(_ context.Context, name string) error {
		if name == "retention_vacuum" {
			return trace.ErrShuttingDown
		}
		return nil
	}
	require.ErrorIs(t, e.p.Pass(context.Background()), trace.ErrShuttingDown)
	sizes, _ := e.store.counts()
	assert.Zero(t, sizes, "the disk cap is not checked after shutdown")
}

func TestPlanner_Pass_ContextCancelledWhileJobRuns(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	e.runner.block = func(jctx context.Context, _ string) error {
		cancel()
		<-jctx.Done()
		return jctx.Err()
	}
	err := e.p.Pass(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"retention_content"}, e.runner.jobNames(), "no job after cancellation")
	assert.Zero(t, e.p.Stats().PassErrors)
}

func TestPlanner_Pass_ConcurrentCallsAreSerialised(t *testing.T) {
	t.Parallel()
	e := newEnv(t, testConfig())
	var (
		mu      sync.Mutex
		running int
		overlap bool
	)
	e.runner.block = func(_ context.Context, name string) error {
		mu.Lock()
		defer mu.Unlock()
		switch name {
		case "retention_content":
			running++
			overlap = overlap || running > 1
		case "retention_checkpoint":
			running--
		}
		return nil
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { assert.NoError(t, e.p.Pass(context.Background())) })
	}
	wg.Wait()
	assert.False(t, overlap)
	assert.Equal(t, int64(4), e.p.Stats().Passes)
}
