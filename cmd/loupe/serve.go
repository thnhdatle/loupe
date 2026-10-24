// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/platform/metrics"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the OTLP receivers, dashboard and API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serve(ctx, cfg, cmd.ErrOrStderr())
		},
	}
}

// serve runs the server described by cfg until ctx ends.
func serve(ctx context.Context, cfg *config.Config, logOut io.Writer) error {
	applyMemoryLimit(cfg)
	log := newLogger(cfg.Log, logOut)
	s, err := newServer(ctx, cfg, log, metrics.BuildInfo{Version: version, Commit: commit, GoVersion: runtime.Version()})
	if err != nil {
		return err
	}
	return s.run(ctx)
}

func applyMemoryLimit(cfg *config.Config) {
	if _, ok := os.LookupEnv("GOMEMLIMIT"); ok || cfg.Runtime.MemoryLimit <= 0 {
		return
	}
	debug.SetMemoryLimit(cfg.Runtime.MemoryLimit.Bytes())
}

func newLogger(c config.Log, w io.Writer) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(c.Level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
