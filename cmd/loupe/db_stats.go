// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
)

func newDBStatsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Print database sizes, row counts and the span time range (read-only, allowed online)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			st, err := sqlite.ReadStats(cmd.Context(), cfg.Storage.Path)
			if err != nil {
				return fmt.Errorf("read database stats: %w", err)
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(st); err != nil {
					return fmt.Errorf("write stats: %w", err)
				}
				return nil
			}
			return writeStats(cmd.OutOrStdout(), st)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func writeStats(w io.Writer, st sqlite.DBStats) error {
	var err error
	p := func(format string, a ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, a...)
		}
	}
	p("path: %s\nschema_version: %d\n", st.Path, st.SchemaVersion)
	p("main_bytes: %d\nwal_bytes: %d\nshm_bytes: %d\n", st.MainBytes, st.WALBytes, st.SHMBytes)
	p("page_size: %d\npage_count: %d\nfreelist_count: %d\n", st.PageSize, st.PageCount, st.FreelistCount)
	for _, t := range st.Tables {
		p("rows.%s: %d\n", t.Table, t.Rows)
	}
	if st.OldestSpan != nil && st.NewestSpan != nil {
		p("oldest_span: %s\nnewest_span: %s\n", st.OldestSpan.Format(time.RFC3339), st.NewestSpan.Format(time.RFC3339))
	} else {
		p("oldest_span: -\nnewest_span: -\n")
	}
	if err != nil {
		return fmt.Errorf("write stats: %w", err)
	}
	return nil
}
