// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/thnhdatle/loupe/internal/adapter/web"
	"github.com/thnhdatle/loupe/internal/app/queries"
)

// newDashboard builds the embedded dashboard. Pages need a session
// only on a non-loopback ui.listen without ui.allow_unauthenticated. With
// api.generate_token every start logs a fresh single-use login URL; the API
// token itself is never logged.
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
		// The single-use login code is the only secret Loupe ever logs: it
		// expires after 15 minutes and works once.
		s.log.Info("dashboard login: "+web.LoginURL(cfg.UI.Listen, cfg.UI.TLS.Enabled(), code),
			"component", "web")
	}
	return dash, nil
}
