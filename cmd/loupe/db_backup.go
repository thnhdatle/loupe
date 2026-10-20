// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
)

func newDBBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup <path>",
		Short: "Write a consistent copy with VACUUM INTO (read-only, allowed online; never overwrites)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if berr := sqlite.BackupFile(cmd.Context(), cfg.Storage.Path, args[0]); berr != nil {
				return fmt.Errorf("backup: %w", berr)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "backup written to %s\n", args[0])
			return err
		},
	}
}
