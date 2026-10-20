// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/app/writer"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const offlinePoll = 20 * time.Millisecond

var errOffline = errors.New("offline run")

// openOffline opens the store while holding the database lock. It fails when a
// running server holds the lock.
func openOffline(ctx context.Context, cfg *config.Config) (*sqlite.Store, error) {
	st, err := sqlite.Open(ctx, cfg.Storage.Path, sqlite.WithBackupBeforeMigrate(cfg.Storage.BackupBeforeMigrate))
	if err != nil {
		if errors.Is(err, sqlite.ErrLocked) {
			return nil, fmt.Errorf("%w: the database is in use; stop the server or omit --offline: %w", errOffline, err)
		}
		return nil, fmt.Errorf("%w: open store: %w", errOffline, err)
	}
	return st, nil
}

// runOffline runs one admin job through the same admin.Service code path as
// the server, on a store opened under the database lock and a local writer.
func runOffline(
	ctx context.Context, cfg *config.Config, logOut io.Writer, start func(*admin.Service) (string, error),
) (admin.JobStatus, error) {
	log := newLogger(cfg.Log, logOut)
	st, err := openOffline(ctx, cfg)
	if err != nil {
		return admin.JobStatus{}, err
	}
	clk := clock.System{}
	w, err := writer.New(st, clk, writer.WithLogger(log), writer.WithShutdownTimeout(cfg.ShutdownTimeout.Std()))
	if err != nil {
		return admin.JobStatus{}, errors.Join(fmt.Errorf("%w: writer: %w", errOffline, err), st.Close())
	}
	wctx, stopWriter := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWriter()
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(wctx) }()
	svc := admin.New(st.Admin(), st, w, clk)

	status, jerr := waitLocal(ctx, svc, start)
	cerr := svc.Close()
	stopWriter()
	werr := <-writerDone
	if jerr == nil {
		log.Info("offline job finished", "component", "admin", "operation", string(status.Kind),
			"job_id", status.ID, "count", status.Rows)
	}
	return status, errors.Join(jerr, cerr, werr, st.Close())
}

func waitLocal(
	ctx context.Context, svc *admin.Service, start func(*admin.Service) (string, error),
) (admin.JobStatus, error) {
	id, err := start(svc)
	if err != nil {
		return admin.JobStatus{}, fmt.Errorf("%w: start job: %w", errOffline, err)
	}
	for {
		js, err := svc.JobStatus(id)
		if err != nil {
			return js, fmt.Errorf("%w: job status: %w", errOffline, err)
		}
		switch js.State {
		case admin.StateDone:
			return js, nil
		case admin.StateFailed:
			return js, fmt.Errorf("%w: job %s failed: %s", errOffline, js.ID, js.Error)
		case admin.StatePending, admin.StateRunning:
		}
		select {
		case <-ctx.Done():
			return js, fmt.Errorf("wait for job %s: %w", id, ctx.Err())
		case <-time.After(offlinePoll):
		}
	}
}
