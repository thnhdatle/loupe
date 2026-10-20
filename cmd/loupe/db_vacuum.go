// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/config"
)

var errUsage = errors.New("usage")

func newDBVacuumCmd() *cobra.Command {
	var (
		af   adminFlags
		full bool
	)
	cmd := &cobra.Command{
		Use:   "vacuum",
		Short: "Reclaim free pages via the running server, or offline with --offline [--full]",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if full && !af.offline {
				return fmt.Errorf("%w: --full requires --offline", errUsage)
			}
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			ctx, stop := commandContext(cmd)
			defer stop()
			var msg string
			switch {
			case af.offline && full:
				msg, err = fullVacuumOffline(ctx, cfg)
			case af.offline:
				msg, err = vacuumOffline(ctx, cfg, cmd)
			default:
				msg, err = vacuumOnline(ctx, cfg, af)
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return err
		},
	}
	af.register(cmd)
	cmd.Flags().BoolVar(&full, "full", false, "run a full VACUUM (needs --offline)")
	return cmd
}

func fullVacuumOffline(ctx context.Context, cfg *config.Config) (string, error) {
	st, err := openOffline(ctx, cfg)
	if err != nil {
		return "", err
	}
	verr := st.FullVacuum(ctx)
	cerr := st.Close()
	if verr != nil {
		return "", fmt.Errorf("full vacuum: %w", verr)
	}
	if cerr != nil {
		return "", fmt.Errorf("close store: %w", cerr)
	}
	return "full vacuum done", nil
}

func vacuumOffline(ctx context.Context, cfg *config.Config, cmd *cobra.Command) (string, error) {
	js, err := runOffline(ctx, cfg, cmd.ErrOrStderr(), func(s *admin.Service) (string, error) { return s.Vacuum() })
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("vacuum done: job %s, %d pages freed", js.ID, js.Rows), nil
}

func vacuumOnline(ctx context.Context, cfg *config.Config, af adminFlags) (string, error) {
	c, err := newAdminClient(cfg, af)
	if err != nil {
		return "", err
	}
	v, err := c.runJob(ctx, "/v1/admin/vacuum", nil)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("vacuum done: job %s, %d pages freed", v.JobID, v.Rows), nil
}
