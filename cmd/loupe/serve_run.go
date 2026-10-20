// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// run serves until ctx ends or a component fails, then shuts down in order:
// stop the receivers and retention, drain the writer, stop the HTTP servers,
// checkpoint, close the store (releasing the flock). It always closes the store.
func (s *server) run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
	)
	fail := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		cancel()
	}

	// The writer and the HTTP servers outlive the receivers: they get contexts
	// that are cancelled only by the shutdown steps below.
	writerCtx, stopWriter := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWriter()
	httpCtx, stopHTTP := context.WithCancel(context.WithoutCancel(ctx))
	defer stopHTTP()

	writerDone := make(chan error, 1)
	go func() { writerDone <- s.writer.Run(writerCtx) }()

	var httpG errgroup.Group
	httpG.Go(func() error {
		fail(serveHTTP(httpCtx, s.uiServer, s.uiLn, s.cfg.UI.TLS.Enabled(), s.shutdownTimeout()))
		return nil
	})
	httpG.Go(func() error { fail(serveHTTP(httpCtx, s.admServer, s.adminLn, false, s.shutdownTimeout())); return nil })

	var recvG errgroup.Group
	recvG.Go(func() error { fail(s.otlp.RunHTTP(runCtx, s.httpLn)); return nil })
	recvG.Go(func() error { fail(s.otlp.RunGRPC(runCtx, s.grpcLn)); return nil })
	recvG.Go(func() error { fail(s.planner.Run(runCtx)); return nil })

	s.ready.Store(true)
	for _, l := range []struct {
		name string
		addr string
	}{
		{"grpc", s.grpcLn.Addr().String()},
		{"http", s.httpLn.Addr().String()},
		{"ui", s.uiLn.Addr().String()},
		{"admin", s.adminLn.Addr().String()},
	} {
		s.log.Info("listening", "component", l.name, "addr", l.addr)
	}
	s.log.Info("loupe serving", "component", "serve", "path", s.cfg.Storage.Path)

	<-runCtx.Done()
	s.log.Info("shutting down", "component", "serve")
	s.ready.Store(false)
	s.otlp.BeginShutdown()
	_ = recvG.Wait()
	if err := s.admin.Close(); err != nil {
		fail(fmt.Errorf("close admin jobs: %w", err))
	}
	stopWriter()
	werr := <-writerDone
	if werr != nil {
		fail(fmt.Errorf("drain writer: %w", werr))
	}
	stopHTTP()
	_ = httpG.Wait()
	if busy, err := s.store.Checkpoint(context.WithoutCancel(ctx)); err != nil {
		fail(fmt.Errorf("checkpoint: %w", err))
	} else if busy {
		s.log.Warn("final checkpoint busy", "component", "serve")
	}
	if err := s.close(); err != nil {
		fail(err)
	}
	s.log.Info("stopped", "component", "serve")
	mu.Lock()
	defer mu.Unlock()
	return firstErr
}

// serveHTTP serves srv on ln until ctx ends, then drains it within timeout.
func serveHTTP(ctx context.Context, srv *http.Server, ln net.Listener, useTLS bool, timeout time.Duration) error {
	errc := make(chan error, 1)
	go func() {
		if useTLS {
			errc <- srv.ServeTLS(ln, "", "")
			return
		}
		errc <- srv.Serve(ln)
	}()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", srv.Addr, err)
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		err := srv.Shutdown(sctx)
		<-errc
		if err != nil {
			return fmt.Errorf("shutdown %s: %w", srv.Addr, err)
		}
		return nil
	}
}

func (s *server) shutdownTimeout() time.Duration { return s.cfg.ShutdownTimeout.Std() }
