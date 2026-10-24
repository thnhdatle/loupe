// SPDX-License-Identifier: Apache-2.0

package admin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

var errBoom = errors.New("boom")

func calculator(t *testing.T) *cost.Calculator {
	t.Helper()
	tbl, err := cost.NewPriceTable(nil, nil)
	require.NoError(t, err)
	c, err := cost.NewCalculator(tbl)
	require.NoError(t, err)
	return c
}

func TestService_Recompute_ChunksThenCheckpoint(t *testing.T) {
	t.Parallel()
	e := newEnv(t, &fakeStore{recompute: 25})
	id, err := e.svc.Recompute(calculator(t), admin.Range{})
	require.NoError(t, err)
	st := e.await(t, id)
	require.Equal(t, admin.StateDone, st.State)
	require.Equal(t, 25, st.Rows)
	require.Equal(t, admin.KindRecompute, st.Kind)
	require.Equal(t, epoch, st.CreatedAt)
	require.False(t, st.FinishedAt.IsZero())
	require.Equal(t, []int64{0, 10, 20}, e.store.afterKeys)
	want := []string{"admin_recompute", "admin_recompute", "admin_recompute", "admin_checkpoint"}
	require.Equal(t, want, e.runner.jobNames())
	require.Equal(t, 1, e.store.checkpoint)
}

func TestService_Recompute_Validation(t *testing.T) {
	t.Parallel()
	e := newEnv(t, &fakeStore{})
	_, err := e.svc.Recompute(nil, admin.Range{})
	require.ErrorIs(t, err, cost.ErrNilTable)
	_, err = e.svc.Recompute(calculator(t), admin.Range{Since: epoch.Add(time.Hour), Until: epoch})
	require.ErrorIs(t, err, admin.ErrInvalidRange)
}

func TestService_DeleteUser_Table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		user    string
		left    int
		errOn   string
		runErr  map[string]error
		wantErr error
		state   admin.State
		rows    int
		ckpt    int
	}{
		{name: "done", user: "u1", left: 15, state: admin.StateDone, rows: 15, ckpt: 1},
		{name: "nothing to delete", user: "u1", state: admin.StateDone, ckpt: 1},
		{name: "empty id", user: "  ", wantErr: admin.ErrEmptyUserID},
		{name: "store error", user: "u1", left: 5, errOn: "delete", state: admin.StateFailed},
		{name: "checkpoint error", user: "u1", left: 5, errOn: "checkpoint", state: admin.StateFailed, rows: 5},
		{name: "vacuum error", user: "u1", left: 5, errOn: "vacuum", state: admin.StateFailed, rows: 5},
		{
			name: "shutting down", user: "u1", left: 5, state: admin.StateFailed,
			runErr: map[string]error{"admin_delete_user": trace.ErrShuttingDown},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, &fakeStore{deleteLeft: tc.left, errOn: tc.errOn})
			e.runner.err = tc.runErr
			id, err := e.svc.DeleteUser(tc.user)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			st := e.await(t, id)
			require.Equal(t, tc.state, st.State)
			require.Equal(t, tc.rows, st.Rows)
			require.Equal(t, tc.ckpt, e.store.checkpoint)
			if tc.state == admin.StateFailed {
				require.NotEmpty(t, st.Error)
			}
		})
	}
}

func TestService_Recompute_StoreError(t *testing.T) {
	t.Parallel()
	e := newEnv(t, &fakeStore{recompute: 5, errOn: "recompute"})
	id, err := e.svc.Recompute(calculator(t), admin.Range{})
	require.NoError(t, err)
	st := e.await(t, id)
	require.Equal(t, admin.StateFailed, st.State)
	require.Contains(t, st.Error, "boom")
}

func TestService_Vacuum(t *testing.T) {
	t.Parallel()
	e := newEnv(t, &fakeStore{freeLeft: 25})
	id, err := e.svc.Vacuum()
	require.NoError(t, err)
	st := e.await(t, id)
	require.Equal(t, admin.StateDone, st.State)
	require.Equal(t, 25, st.Rows)
	require.Equal(t, 1, e.store.checkpoint)
}

func TestService_Backup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		err     error
		wantErr error
		state   admin.State
	}{
		{name: "ok", path: "/tmp/b.db", state: admin.StateDone},
		{name: "empty", path: "", wantErr: admin.ErrInvalidPath},
		{name: "nul", path: "a\x00b", wantErr: admin.ErrInvalidPath},
		{name: "backuper fails", path: "/tmp/b.db", err: errBoom, state: admin.StateFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, &fakeStore{})
			e.backup.err = tc.err
			id, err := e.svc.Backup(tc.path)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.state, e.await(t, id).State)
			require.Equal(t, []string{tc.path}, e.backup.paths)
			require.Empty(t, e.runner.jobNames(), "backup must not use the writer")
		})
	}
}

func TestBackupPath(t *testing.T) {
	t.Parallel()
	got := admin.BackupPath("/data", time.Date(2026, 10, 1, 12, 30, 5, 0, time.UTC))
	require.Equal(t, "/data/backups/loupe-20261001T123005Z.db", got)
}

func TestService_JobStatus_Unknown(t *testing.T) {
	t.Parallel()
	e := newEnv(t, &fakeStore{})
	_, err := e.svc.JobStatus("nope")
	require.ErrorIs(t, err, admin.ErrJobNotFound)
	require.ErrorIs(t, err, trace.ErrNotFound)
}

func TestService_Registry_CapEvictsFinished(t *testing.T) {
	t.Parallel()
	e := newEnv(t, &fakeStore{}, admin.WithMaxJobs(2))
	first, err := e.svc.Vacuum()
	require.NoError(t, err)
	e.await(t, first)
	second, err := e.svc.Vacuum()
	require.NoError(t, err)
	e.await(t, second)
	third, err := e.svc.Vacuum()
	require.NoError(t, err)
	e.await(t, third)
	_, err = e.svc.JobStatus(first)
	require.ErrorIs(t, err, admin.ErrJobNotFound)
	_, err = e.svc.JobStatus(third)
	require.NoError(t, err)
}

func TestService_Registry_FullOfUnfinished(t *testing.T) {
	t.Parallel()
	e := newEnv(t, &fakeStore{}, admin.WithMaxJobs(1))
	e.runner.gate = make(chan struct{})
	id, err := e.svc.Vacuum()
	require.NoError(t, err)
	_, err = e.svc.Vacuum()
	require.ErrorIs(t, err, admin.ErrTooManyJobs)
	close(e.runner.gate)
	require.Equal(t, admin.StateDone, e.await(t, id).State)
}

func TestService_Close_CancelsAndWaits(t *testing.T) {
	t.Parallel()
	e := newEnv(t, &fakeStore{})
	e.runner.gate = make(chan struct{})
	id, err := e.svc.Vacuum()
	require.NoError(t, err)
	require.NoError(t, e.svc.Close())
	st, err := e.svc.JobStatus(id)
	require.NoError(t, err)
	require.Equal(t, admin.StateFailed, st.State)
	_, err = e.svc.Vacuum()
	require.ErrorIs(t, err, admin.ErrClosed)
}

func TestService_DefaultIDAndOptions(t *testing.T) {
	t.Parallel()
	svc := admin.New(&fakeStore{}, &fakeBackuper{}, &fakeRunner{}, clock.NewFake(epoch),
		admin.WithBatch(0), admin.WithMaxJobs(0))
	defer func() { require.NoError(t, svc.Close()) }()
	id, err := svc.Vacuum()
	require.NoError(t, err)
	require.Regexp(t, `^job_[0-9a-f]{16}$`, id)
}
