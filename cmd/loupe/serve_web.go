// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/thnhdatle/loupe/internal/adapter/web"
	"github.com/thnhdatle/loupe/internal/app/queries"
)

func (s *server) newDashboard(q *queries.Service, apiTokens []string) (*web.Handler, error) {
	cfg := s.cfg
	dash, err := web.NewHandler(q, web.Options{
		Tokens:       apiTokens,
		RequireLogin: len(apiTokens) > 0 && !isLoopback(cfg.UI.Listen) && !cfg.UI.AllowUnauthenticated,
		SessionTTL:   cfg.UI.SessionTTL.Std(),
		SecureCookie: cfg.SecureCookieEnabled(),
		Clock:        s.clk,
		Logger:       s.log,
	})
	if err != nil {
		return nil, fmt.Errorf("dashboard handler: %w", err)
	}
	if cfg.API.GenerateToken {
		code, err := dash.NewLoginCode()
		if err != nil {
			return nil, fmt.Errorf("dashboard login code: %w", err)
		}
		s.log.Info("dashboard login: "+web.LoginURL(cfg.UI.Listen, cfg.UI.TLS.Enabled(), code),
			"component", "web")
	}
	return dash, nil
}
