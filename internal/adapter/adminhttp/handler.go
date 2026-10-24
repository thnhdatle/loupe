// SPDX-License-Identifier: Apache-2.0

package adminhttp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

const DefaultMaxBodyBytes = 4 << 10

var ErrInvalidOptions = errors.New("adminhttp: invalid options")

const (
	CodeUnauthorized     = "unauthorized"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInvalidParameter = "invalid_parameter"
	CodeInvalidRange     = "invalid_range"
	CodeBodyTooLarge     = "body_too_large"
	CodeTooManyJobs      = "too_many_jobs"
	CodeShuttingDown     = "shutting_down"
	CodeInternal         = "internal"
)

type Admin interface {
	Recompute(calc *cost.Calculator, r admin.Range) (jobID string, err error)
	Vacuum() (jobID string, err error)
	Backup(path string) (jobID string, err error)
	JobStatus(id string) (admin.JobStatus, error)
}

type Options struct {
	Tokens               []string
	AllowUnauthenticated bool
	StorageDir           string
	Calculator           func() (*cost.Calculator, error)
	MaxBodyBytes         int64
	Clock                clock.Clock
	Logger               *slog.Logger
	AccessLog            bool
	Observe              httpmw.Observer
}

// Handler serves the admin API.
type Handler struct {
	svc     Admin
	opts    Options
	clk     clock.Clock
	bearer  *httpmw.Bearer
	mux     *httpmw.Router
	root    http.Handler
	maxBody int64
}

// NewHandler returns the admin handler.
func NewHandler(svc Admin, opts Options) (*Handler, error) {
	if svc == nil {
		return nil, fmt.Errorf("%w: nil admin service", ErrInvalidOptions)
	}
	if opts.StorageDir == "" || opts.Calculator == nil {
		return nil, fmt.Errorf("%w: storage dir and calculator are required", ErrInvalidOptions)
	}
	h := &Handler{
		svc: svc, opts: opts, clk: opts.Clock, bearer: httpmw.NewBearer(opts.Tokens),
		mux: httpmw.NewRouter(), maxBody: DefaultMaxBodyBytes,
	}
	if !h.bearer.Enabled() && !opts.AllowUnauthenticated {
		return nil, fmt.Errorf("%w: no token configured and unauthenticated access not allowed", ErrInvalidOptions)
	}
	if h.clk == nil {
		h.clk = clock.System{}
	}
	if opts.MaxBodyBytes > 0 {
		h.maxBody = opts.MaxBodyBytes
	}
	h.mux.Handle(http.MethodPost, "/v1/admin/pricing/recompute", h.recompute)
	h.mux.Handle(http.MethodPost, "/v1/admin/vacuum", h.vacuum)
	h.mux.Handle(http.MethodPost, "/v1/admin/backups", h.backup)
	h.mux.Handle(http.MethodGet, "/v1/admin/jobs/{id}", h.job)
	var accessLog *slog.Logger
	if opts.AccessLog {
		accessLog = opts.Logger
	}
	h.root = httpmw.Chain(http.HandlerFunc(h.serveGuarded),
		httpmw.Recover(opts.Logger),
		httpmw.RequestID(h.clk),
		httpmw.Observe(h.clk, opts.Observe, accessLog),
	)
	return h, nil
}

func (h *Handler) Routes() []string { return h.mux.Routes() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.root.ServeHTTP(w, r) }

func (h *Handler) serveGuarded(w http.ResponseWriter, r *http.Request) {
	if !h.opts.AllowUnauthenticated && !h.bearer.Check(r.Header.Get("Authorization")) {
		httpmw.Unauthorized(w, r)
		return
	}
	if r.ContentLength > h.maxBody {
		httpmw.WriteError(w, r, http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "request body too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBody)
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, CodeInternal
	switch {
	case errors.Is(err, admin.ErrJobNotFound), errors.Is(err, trace.ErrNotFound):
		status, code = http.StatusNotFound, CodeNotFound
	case errors.Is(err, admin.ErrInvalidRange):
		status, code = http.StatusBadRequest, CodeInvalidRange
	case errors.Is(err, admin.ErrTooManyJobs):
		status, code = http.StatusTooManyRequests, CodeTooManyJobs
	case errors.Is(err, admin.ErrClosed):
		status, code = http.StatusServiceUnavailable, CodeShuttingDown
	}
	msg := "internal error"
	if status < http.StatusInternalServerError {
		msg = err.Error()
	} else if h.opts.Logger != nil && code == CodeInternal {
		h.opts.Logger.ErrorContext(r.Context(), "admin request failed", "request_id", httpmw.RequestIDFrom(r.Context()),
			"path", r.URL.Path, "error", err)
	}
	httpmw.WriteError(w, r, status, code, msg)
}
