// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/config"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "loupe",
		Short:         "Self-hosted observability for LLM applications",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to the YAML config file (default $"+config.ConfigEnv+")")
	root.AddCommand(
		newVersionCmd(),
		newServeCmd(),
		newHealthcheckCmd(),
		newConfigCmd(),
		newDBCmd(),
		newPricingCmd(),
		newExportCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printVersion(cmd.OutOrStdout())
		},
	}
}

func printVersion(w io.Writer) error {
	_, err := fmt.Fprintf(w, "version: %s\ncommit: %s\nbuild_date: %s\ngo_version: %s\n",
		version, commit, buildDate, runtime.Version())
	return err
}

// commandContext returns the command context cancelled by SIGINT or SIGTERM.
func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
}
