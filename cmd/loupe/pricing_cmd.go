// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/adapter/pricingfile"
	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/config"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const staleAfterDays = 90

func newPricingCmd() *cobra.Command {
	g := &cobra.Command{Use: "pricing", Short: "Manage the price table"}
	g.AddCommand(newPricingListCmd(), newPricingRecomputeCmd())
	return g
}

type priceRow struct {
	Origin        string  `json:"origin"`
	Match         string  `json:"match"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	Input         string  `json:"input_per_mtok"`
	Output        string  `json:"output_per_mtok"`
	CacheRead     *string `json:"cache_read_per_mtok,omitempty"`
	CacheWrite    *string `json:"cache_write_per_mtok,omitempty"`
	Reasoning     *string `json:"reasoning_per_mtok,omitempty"`
	EffectiveFrom string  `json:"effective_from"`
	AsOf          string  `json:"as_of"`
	Source        string  `json:"source"`
	AgeDays       int     `json:"age_days"`
	Stale         bool    `json:"stale"`
}

func priceRows(entries []cost.Price, now time.Time) []priceRow {
	opt := func(v *int64) *string {
		if v == nil {
			return nil
		}
		s := cost.FormatPrice(*v)
		return &s
	}
	rows := make([]priceRow, 0, len(entries))
	for _, e := range entries {
		age := int(now.Sub(e.AsOf).Hours() / 24)
		rows = append(rows, priceRow{
			Origin: string(e.Origin), Match: string(e.Match), Provider: e.Provider, Model: e.Model,
			Input: cost.FormatPrice(e.InputPerMtok), Output: cost.FormatPrice(e.OutputPerMtok),
			CacheRead: opt(e.CacheReadPerMtok), CacheWrite: opt(e.CacheWritePerMtok), Reasoning: opt(e.ReasoningPerMtok),
			EffectiveFrom: e.EffectiveFrom.UTC().Format(time.RFC3339), AsOf: e.AsOf.UTC().Format(time.RFC3339),
			Source: e.Source, AgeDays: age, Stale: age > staleAfterDays,
		})
	}
	return rows
}

func newPricingListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the effective price table and flag entries not verified for 90 days",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			table, err := pricingfile.Load(cfg.Pricing.File)
			if err != nil {
				return fmt.Errorf("load pricing: %w", err)
			}
			rows := priceRows(table.Entries(), clock.System{}.Now())
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(rows); err != nil {
					return fmt.Errorf("write prices: %w", err)
				}
				return nil
			}
			return writePriceRows(cmd.OutOrStdout(), rows)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func writePriceRows(w io.Writer, rows []priceRow) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ORIGIN\tMATCH\tPROVIDER\tMODEL\tINPUT\tOUTPUT\tCACHE_READ\tCACHE_WRITE\t"+
		"REASONING\tEFFECTIVE_FROM\tAS_OF\tAGE_DAYS\tSOURCE")
	dash := func(s *string) string {
		if s == nil {
			return "-"
		}
		return *s
	}
	for _, r := range rows {
		age := strconv.Itoa(r.AgeDays)
		if r.Stale {
			age += " STALE"
		}
		provider := r.Provider
		if provider == "" {
			provider = "*"
		}
		_, _ = fmt.Fprintln(tw, strings.Join([]string{
			r.Origin, r.Match, provider, r.Model, r.Input, r.Output,
			dash(r.CacheRead), dash(r.CacheWrite), dash(r.Reasoning),
			r.EffectiveFrom, r.AsOf, age, r.Source,
		}, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write prices: %w", err)
	}
	return nil
}

func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.ParseInLocation(time.DateOnly, s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q is neither RFC 3339 nor YYYY-MM-DD", errUsage, s)
	}
	return t, nil
}

func newPricingRecomputeCmd() *cobra.Command {
	var (
		af           adminFlags
		since, until string
	)
	cmd := &cobra.Command{
		Use:   "recompute --since <date> [--until <date>]",
		Short: "Reprice stored spans with the current price table",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rng, err := parseRange(since, until)
			if err != nil {
				return err
			}
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			var id string
			var rows int
			if af.offline {
				id, rows, err = recomputeOffline(cmd, cfg, rng)
			} else {
				id, rows, err = recomputeOnline(cmd, cfg, af, rng)
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "recompute done: job %s, %d spans examined\n", id, rows)
			return err
		},
	}
	af.register(cmd)
	cmd.Flags().StringVar(&since, "since", "", "first span start time to reprice (RFC 3339 or YYYY-MM-DD UTC), required")
	cmd.Flags().StringVar(&until, "until", "", "span start time to stop before (RFC 3339 or YYYY-MM-DD UTC)")
	return cmd
}

func parseRange(since, until string) (admin.Range, error) {
	var r admin.Range
	if since == "" {
		return r, fmt.Errorf("%w: --since is required", errUsage)
	}
	var err error
	if r.Since, err = parseDate(since); err != nil {
		return r, err
	}
	if until != "" {
		if r.Until, err = parseDate(until); err != nil {
			return r, err
		}
		if !r.Since.Before(r.Until) {
			return r, fmt.Errorf("%w: --since must be before --until", errUsage)
		}
	}
	return r, nil
}

func recomputeOnline(cmd *cobra.Command, cfg *config.Config, af adminFlags, r admin.Range) (string, int, error) {
	c, err := newAdminClient(cfg, af)
	if err != nil {
		return "", 0, err
	}
	body := map[string]string{"since": r.Since.Format(time.RFC3339)}
	if !r.Until.IsZero() {
		body["until"] = r.Until.Format(time.RFC3339)
	}
	ctx, stop := commandContext(cmd)
	defer stop()
	v, err := c.runJob(ctx, "/v1/admin/pricing/recompute", body)
	return v.JobID, v.Rows, err
}

func recomputeOffline(cmd *cobra.Command, cfg *config.Config, r admin.Range) (string, int, error) {
	table, err := pricingfile.Load(cfg.Pricing.File)
	if err != nil {
		return "", 0, fmt.Errorf("load pricing: %w", err)
	}
	calc, err := cost.NewCalculator(table)
	if err != nil {
		return "", 0, fmt.Errorf("build cost calculator: %w", err)
	}
	ctx, stop := commandContext(cmd)
	defer stop()
	js, err := runOffline(ctx, cfg, cmd.ErrOrStderr(), func(s *admin.Service) (string, error) {
		return s.Recompute(calc, r)
	})
	return js.ID, js.Rows, err
}
