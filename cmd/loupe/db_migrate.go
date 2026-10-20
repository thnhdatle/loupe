// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
)

func newDBCmd() *cobra.Command {
	g := &cobra.Command{Use: "db", Short: "Database maintenance"}
	g.AddCommand(newDBMigrateCmd(), newDBStatsCmd(), newDBVacuumCmd(), newDBBackupCmd())
	return g
}

func newDBMigrateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending schema migrations (offline) or list them with --dry-run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			st, err := sqlite.Migrations(ctx, cfg.Storage.Path)
			if err != nil {
				return fmt.Errorf("read migration status: %w", err)
			}
			out := cmd.OutOrStdout()
			if perr := printMigrationStatus(out, st); perr != nil {
				return perr
			}
			if dryRun || len(st.Pending) == 0 {
				return nil
			}
			store, err := sqlite.Open(ctx, cfg.Storage.Path,
				sqlite.WithBackupBeforeMigrate(cfg.Storage.BackupBeforeMigrate))
			if err != nil {
				if errors.Is(err, sqlite.ErrLocked) {
					return fmt.Errorf("db migrate is offline only; stop the running server first: %w", err)
				}
				return fmt.Errorf("migrate: %w", err)
			}
			if cerr := store.Close(); cerr != nil {
				return fmt.Errorf("close store: %w", cerr)
			}
			_, err = fmt.Fprintf(out, "migrated to version %d\n", st.Target)
			return err
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report pending migrations without applying them")
	return cmd
}

func printMigrationStatus(w io.Writer, st sqlite.MigrationStatus) error {
	if _, err := fmt.Fprintf(w, "current version: %d\ntarget version: %d\n", st.Current, st.Target); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	for _, v := range st.Pending {
		if _, err := fmt.Fprintf(w, "pending: %d\n", v); err != nil {
			return fmt.Errorf("write status: %w", err)
		}
	}
	return nil
}
