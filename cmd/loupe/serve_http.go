// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// readyProbeTimeout bounds the writer round trip of /readyz.
const readyProbeTimeout = 2 * time.Second

// adminMux serves the admin listener: /healthz, /readyz, /metrics and, with
// debug.pprof, /debug/pprof/*. It has no mutating endpoints.
func (s *server) adminMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", s.metrics.Handler())
	if s.cfg.Debug.Pprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	return mux
}

// readyz answers 200 once migrations are applied (the store opened), the writer
// runs and shutdown has not begun. The writer is probed with a no-op job, which
// proves its goroutine takes work.
func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.checkReady(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ready\n"))
}

func (s *server) checkReady(ctx context.Context) error {
	if !s.ready.Load() {
		return errNotReady
	}
	ctx, cancel := context.WithTimeout(ctx, readyProbeTimeout)
	defer cancel()
	return s.writer.Submit(ctx, trace.Job{Name: "readiness", Run: func(context.Context) error { return nil }})
}
