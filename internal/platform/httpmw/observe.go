// SPDX-License-Identifier: Apache-2.0

package httpmw

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// Observer receives one call per finished request: the matched route pattern
// (bounded cardinality), the status and the duration.
type Observer func(endpoint string, status int, d time.Duration)

// Observe reports every request to obs and, when log is non-nil, writes an
// access log line. With both nil it returns next unchanged.
func Observe(clk clock.Clock, obs Observer, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		if obs == nil && log == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clk.Now()
			rec := &StatusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.Status == 0 {
				rec.Status = http.StatusOK
			}
			d := clk.Now().Sub(start)
			if obs != nil {
				ep := r.Pattern
				if ep == "" {
					ep = "unmatched"
				}
				obs(ep, rec.Status, d)
			}
			if log != nil {
				log.InfoContext(r.Context(), "http request", "request_id", RequestIDFrom(r.Context()),
					"method", r.Method, "path", r.URL.Path, "status", rec.Status, "duration_ms", d.Milliseconds())
			}
		})
	}
}
