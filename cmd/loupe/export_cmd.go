// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/query"
)

func newExportCmd() *cobra.Command {
	var from, to, out, format string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export traces as JSON Lines (read-only, allowed online)",
		Long: "Export the traces starting in [--from, --to) as JSON Lines, one trace per line, in the format of\n" +
			"GET /v1/export/traces. The database is opened read-only without its lock, so this works while\n" +
			"the server runs. The default range is the last 24 hours.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != httpapi.ExportFormatJSONL {
				return fmt.Errorf("%w: --format must be %q", errUsage, httpapi.ExportFormatJSONL)
			}
			req, err := exportRange(from, to, time.Now())
			if err != nil {
				return err
			}
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			ctx, stop := commandContext(cmd)
			defer stop()
			return runExport(ctx, cfg.Storage.Path, req, out, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "range start, RFC 3339 or YYYY-MM-DD UTC (default: --to minus 24h)")
	cmd.Flags().StringVar(&to, "to", "", "range end (exclusive), RFC 3339 or YYYY-MM-DD UTC (default: now)")
	cmd.Flags().StringVar(&out, "out", "", "write to this file instead of stdout; it must not exist")
	cmd.Flags().StringVar(&format, "format", httpapi.ExportFormatJSONL, "output format (only \"jsonl\")")
	return cmd
}

// exportRange resolves the --from/--to flags to a request.
func exportRange(from, to string, now time.Time) (query.ExportRequest, error) {
	var fp, tp *time.Time
	if from != "" {
		t, err := parseDate(from)
		if err != nil {
			return query.ExportRequest{}, err
		}
		fp = &t
	}
	if to != "" {
		t, err := parseDate(to)
		if err != nil {
			return query.ExportRequest{}, err
		}
		tp = &t
	}
	start, end, err := query.ResolveRange(fp, tp, now)
	if err != nil {
		return query.ExportRequest{}, fmt.Errorf("%w: %w", errUsage, err)
	}
	return query.ExportRequest{From: start, To: end}, nil
}

// runExport streams the traces of req from the database at dbPath to the file
// out, or to stdout when out is empty.
func runExport(ctx context.Context, dbPath string, req query.ExportRequest, out string, stdout io.Writer) (err error) {
	w := stdout
	if out != "" {
		f, ferr := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // operator-chosen path
		if ferr != nil {
			return fmt.Errorf("create %s (it must not exist): %w", out, ferr)
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && err == nil {
				err = fmt.Errorf("close %s: %w", out, cerr)
			}
			if err != nil {
				_ = os.Remove(out)
			}
		}()
		w = f
	}
	st, err := sqlite.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open database read-only: %w", err)
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	if err := st.ExportTraces(ctx, req, func(b []query.TraceDetail) error {
		return httpapi.EncodeExportLines(w, b)
	}); err != nil {
		return fmt.Errorf("export traces: %w", err)
	}
	return nil
}
