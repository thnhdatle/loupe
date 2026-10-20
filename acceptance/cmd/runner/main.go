// SPDX-License-Identifier: Apache-2.0

// Command runner executes the acceptance criteria of criteria.yaml and writes
// acceptance/report.md. Run it from the repository root.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/runner"
)

func main() {
	os.Exit(run())
}

func run() int {
	opts, err := runner.ParseArgs(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "runner:", err)
		return 2
	}
	if opts.Root, err = criteria.FindRoot(wd); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code, err := runner.New(opts, runner.OSExecutor{}, os.Getenv, os.Stdout).Run(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return code
}
