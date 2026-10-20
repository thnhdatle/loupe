// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/thnhdatle/loupe/bench/load"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		cfg load.Config
		out string
	)
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.StringVar(&cfg.HTTPURL, "http", "http://127.0.0.1:4318", "OTLP/HTTP base URL")
	fs.StringVar(&cfg.GRPCAddr, "grpc", "127.0.0.1:4317", "OTLP/gRPC host:port")
	fs.Float64Var(&cfg.Rate, "rate", 3000, "arrival rate in spans per second")
	fs.DurationVar(&cfg.Duration, "duration", 10*time.Minute, "load duration")
	fs.IntVar(&cfg.Batch, "batch", 512, "spans per request")
	fs.Uint64Var(&cfg.Seed, "seed", 1, "payload seed")
	run := fs.Uint64("run", 1, "run id embedded in trace and span ids")
	fs.StringVar(&cfg.Mode, "mode", load.ModeReject, "overflow mode for partial success: reject or drop")
	fs.StringVar(&cfg.Token, "token", "", "bearer token")
	fs.Float64Var(&cfg.GRPCFraction, "grpc-fraction", 0, "share of requests sent over gRPC")
	fs.IntVar(&cfg.MaxInflight, "max-inflight", 1024, "cap on requests in flight")
	fs.StringVar(&out, "out", "", "write the JSON summary to this file")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	cfg.RunID = uint32(*run) //nolint:gosec // run ids are small.
	cfg.Progress = os.Stderr

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	sum, err := load.Run(ctx, cfg)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	if out != "" {
		return os.WriteFile(out, append(b, '\n'), 0o600)
	}
	return nil
}
