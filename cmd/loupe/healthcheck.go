// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

const healthcheckTimeout = 3 * time.Second

var errUnhealthy = errors.New("unhealthy")

func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 when the admin /readyz returns 200",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			hp, err := dialHost(cfg.Admin.Listen)
			if err != nil {
				return err
			}
			return checkReady(cmd.Context(), "http://"+hp+"/readyz")
		},
	}
}

func checkReady(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", errUnhealthy, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s returned %d", errUnhealthy, url, resp.StatusCode)
	}
	return nil
}
