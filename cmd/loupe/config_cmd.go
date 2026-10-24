// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/config"
)

func newConfigCmd() *cobra.Command {
	g := &cobra.Command{Use: "config", Short: "Validate or print configuration"}
	g.AddCommand(newConfigValidateCmd(), newConfigPrintCmd())
	return g
}

func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("read --config: %w", err)
	}
	return config.Load(path)
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Load the configuration and report every problem (exit 2 when invalid)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if cfg.Pricing.File != "" {
				if _, perr := pricingfile.Load(cfg.Pricing.File); perr != nil {
					return fmt.Errorf("%w: pricing.file %q: %w", config.ErrInvalid, cfg.Pricing.File, perr)
				}
			}
			for _, w := range cfg.Warnings() {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "configuration is valid")
			return err
		},
	}
}

func newConfigPrintCmd() *cobra.Command {
	var redact bool
	cmd := &cobra.Command{
		Use:   "print",
		Short: "Print the effective configuration as YAML",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if redact {
				r := cfg.Redact()
				cfg = &r
			}
			return cfg.WriteYAML(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&redact, "redact", false, "mask token files, key files and endpoint credentials")
	return cmd
}

func exitCode(err error) int {
	if errors.Is(err, config.ErrInvalid) {
		return 2
	}
	return 1
}
