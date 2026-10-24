// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/thnhdatle/loupe/internal/app/analyze"
	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

const (
	DefaultMaxQueryBytes = 8 << 10
	DefaultMaxBodyBytes  = 4 << 10
)

var ErrInvalidOptions = errors.New("httpapi: invalid options")

type Queries interface {
	ListTraces(ctx context.Context, req queries.TraceListRequest) (query.TracePage, error)
	GetTrace(ctx context.Context, id string, includeContent bool) (query.TraceDetail, error)
	ListSessions(ctx context.Context, req queries.SessionListRequest) (query.SessionPage, error)
	GetSession(ctx context.Context, id string, p query.Page) (query.SessionDetail, error)
	Overview(ctx context.Context, req query.OverviewRequest) (queries.Overview, error)
	TopSpans(ctx context.Context, req query.TopSpansRequest) ([]query.TopSpan, error)
	IngestStats(ctx context.Context) (query.IngestStats, error)
}

type Analyzer interface {
	Analyze(ctx context.Context, traceID string) (analyze.Report, error)
	Timeout() time.Duration
}

type UserDeleter interface {
	DeleteUser(userID string) (jobID string, err error)
}

type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
}

type Options struct {
	Tokens               []string
	AllowUnauthenticated bool
	SessionAuth          func(r *http.Request) bool
	Exporter             query.TraceExporter
	Analyzer             Analyzer
	Version              VersionInfo
	MaxQueryBytes        int
	MaxBodyBytes         int64
	Clock                clock.Clock
	Logger               *slog.Logger
	AccessLog            bool
	Observe              httpmw.Observer
}

// Handler serves the REST /v1 API.
type Handler struct {
	q        Queries
	deleter  UserDeleter
	opts     Options
	clk      clock.Clock
	bearer   *httpmw.Bearer
	mux      *httpmw.Router
	root     http.Handler
	maxQuery int
	maxBody  int64
}

// NewHandler returns the REST /v1 handler.
func NewHandler(q Queries, deleter UserDeleter, opts Options) (*Handler, error) {
	if q == nil {
		return nil, fmt.Errorf("%w: nil queries", ErrInvalidOptions)
	}
	h := &Handler{
		q: q, deleter: deleter, opts: opts, bearer: httpmw.NewBearer(opts.Tokens),
		mux:      httpmw.NewRouter(),
		maxQuery: DefaultMaxQueryBytes, maxBody: DefaultMaxBodyBytes,
	}
	if !h.bearer.Enabled() && !opts.AllowUnauthenticated {
		return nil, fmt.Errorf("%w: no token configured and unauthenticated access not allowed", ErrInvalidOptions)
	}
	if opts.MaxQueryBytes > 0 {
		h.maxQuery = opts.MaxQueryBytes
	}
	if opts.MaxBodyBytes > 0 {
		h.maxBody = opts.MaxBodyBytes
	}
	if h.opts.Version.GoVersion == "" {
		h.opts.Version.GoVersion = runtime.Version()
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.System{}
	}
	h.clk = clk
	h.routes()
	var accessLog *slog.Logger
	if opts.AccessLog {
		accessLog = opts.Logger
	}
	h.root = httpmw.Chain(http.HandlerFunc(h.serveLimited),
		httpmw.Recover(opts.Logger),
		httpmw.RequestID(clk),
		httpmw.Observe(clk, opts.Observe, accessLog),
	)
	return h, nil
}

func (h *Handler) Routes() []string { return h.mux.Routes() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.root.ServeHTTP(w, r) }

func (h *Handler) routes() {
	h.mux.Handle(http.MethodGet, "/v1/traces", h.listTraces)
	h.mux.Handle(http.MethodGet, "/v1/traces/{trace_id}", h.getTrace)
	h.mux.Handle(http.MethodPost, "/v1/traces/{trace_id}/analyze", h.analyzeTrace)
	h.mux.Handle(http.MethodGet, "/v1/sessions", h.listSessions)
	h.mux.Handle(http.MethodGet, "/v1/sessions/{id}", h.getSession)
	h.mux.Handle(http.MethodGet, "/v1/stats/overview", h.overview)
	h.mux.Handle(http.MethodGet, "/v1/stats/top-spans", h.topSpans)
	h.mux.Handle(http.MethodGet, "/v1/ingest/stats", h.ingestStats)
	h.mux.Handle(http.MethodGet, "/v1/export/traces", h.exportTraces)
	h.mux.Handle(http.MethodDelete, "/v1/users/{user_id}/data", h.deleteUserData)
	h.mux.Handle(http.MethodGet, "/v1/version", h.version)
}

func (h *Handler) serveLimited(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		httpmw.Unauthorized(w, r)
		return
	}
	if len(r.URL.RawQuery) > h.maxQuery {
		httpmw.WriteError(w, r, http.StatusRequestURITooLong, CodeURITooLong, "query string too long")
		return
	}
	if r.ContentLength > h.maxBody {
		httpmw.WriteError(w, r, http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "request body too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBody)
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) authorized(r *http.Request) bool {
	if h.bearer.Check(r.Header.Get("Authorization")) {
		return true
	}
	if h.opts.AllowUnauthenticated {
		return true
	}
	if h.opts.SessionAuth != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		return h.opts.SessionAuth(r)
	}
	return false
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var pe *paramError
	if errors.As(err, &pe) {
		httpmw.WriteError(w, r, http.StatusBadRequest, CodeInvalidParameter, pe.Error())
		return
	}
	status, code, expose := classify(err)
	msg := "internal error"
	if expose {
		msg = err.Error()
	} else if h.opts.Logger != nil {
		h.opts.Logger.ErrorContext(r.Context(), "api request failed", "request_id", httpmw.RequestIDFrom(r.Context()),
			"path", r.URL.Path, "error", err)
	}
	httpmw.WriteError(w, r, status, code, msg)
}
