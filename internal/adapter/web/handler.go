// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
	assets "github.com/thnhdatle/loupe/web"
)

// ErrInvalidOptions reports unusable Options or a missing dependency.
var ErrInvalidOptions = errors.New("web: invalid options")

// DefaultSessionTTL is the session lifetime when Options.SessionTTL is unset.
const DefaultSessionTTL = 12 * time.Hour

// SessionCookie is the name of the dashboard session cookie.
const SessionCookie = "loupe_session"

// layoutTemplate is the entry template of every full page.
const layoutTemplate = "layout"

// trueValue is the header and form value for "yes".
const trueValue = "true"

// pageLimit is the page size of the trace and session lists.
const pageLimit = 50

// Queries is the read side the dashboard needs, implemented by *queries.Service.
type Queries interface {
	ListTraces(ctx context.Context, req queries.TraceListRequest) (query.TracePage, error)
	GetTrace(ctx context.Context, id string, includeContent bool) (query.TraceDetail, error)
	ListSessions(ctx context.Context, req queries.SessionListRequest) (query.SessionPage, error)
	GetSession(ctx context.Context, id string, p query.Page) (query.SessionDetail, error)
	Overview(ctx context.Context, req query.OverviewRequest) (queries.Overview, error)
	ModelStats(ctx context.Context, from, to time.Time) ([]queries.GroupStats, error)
	IngestInRange(ctx context.Context, from, to time.Time) (query.IngestStats, error)
}

// Options configures the dashboard handler.
type Options struct {
	// Tokens are the API tokens POST /login accepts (api.token_file).
	Tokens []string
	// RequireLogin makes every page need a session. The caller sets it when
	// the UI listener is not loopback and ui.allow_unauthenticated is false.
	RequireLogin bool
	// SessionTTL is the session lifetime (ui.session_ttl); zero means DefaultSessionTTL.
	SessionTTL time.Duration
	// SecureCookie sets the Secure cookie flag (ui.tls or ui.secure_cookie).
	SecureCookie bool
	// Clock supplies time for expiry and rate limiting; nil uses the system clock.
	Clock clock.Clock
	// Logger receives internal errors; nil discards them.
	Logger *slog.Logger
	// Rand is the source of session ids, login codes and nonces; nil uses crypto/rand.
	Rand io.Reader
}

// Handler serves the dashboard at "/" plus /login, /logout and /static/.
type Handler struct {
	q        Queries
	opts     Options
	ttl      time.Duration
	bearer   *httpmw.Bearer
	clk      clock.Clock
	log      *slog.Logger
	rnd      *randSource
	sessions *expiringSet
	codes    *expiringSet
	limiter  *loginLimiter
	pages    map[string]*template.Template
	root     http.Handler
}

// NewHandler returns the dashboard handler reading from q.
func NewHandler(q Queries, opts Options) (*Handler, error) {
	if q == nil {
		return nil, fmt.Errorf("%w: nil queries", ErrInvalidOptions)
	}
	h := &Handler{q: q, opts: opts, ttl: opts.SessionTTL, bearer: httpmw.NewBearer(opts.Tokens)}
	if h.ttl < 0 {
		return nil, fmt.Errorf("%w: negative session ttl", ErrInvalidOptions)
	}
	if h.ttl == 0 {
		h.ttl = DefaultSessionTTL
	}
	if h.clk = opts.Clock; h.clk == nil {
		h.clk = clock.System{}
	}
	if h.log = opts.Logger; h.log == nil {
		h.log = slog.New(slog.DiscardHandler)
	}
	h.rnd = &randSource{r: opts.Rand}
	if h.rnd.r == nil {
		h.rnd.r = rand.Reader
	}
	h.sessions, h.codes, h.limiter = newExpiringSet(h.clk), newExpiringSet(h.clk), newLoginLimiter(h.clk)
	var err error
	if h.pages, err = parsePages(assets.FS); err != nil {
		return nil, err
	}
	h.root = httpmw.Chain(h.routes(), httpmw.Recover(h.log), httpmw.RequestID(h.clk), h.secure)
	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.root.ServeHTTP(w, r) }

func (h *Handler) routes() http.Handler {
	static, err := fs.Sub(assets.FS, "static")
	if err != nil {
		// Unreachable: "static" is embedded by go:embed.
		panic(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(http.FileServerFS(static))))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /{$}", h.page(h.overview))
	mux.HandleFunc("GET /traces", h.page(h.traces))
	mux.HandleFunc("GET /traces/{id}", h.page(h.traceDetail))
	mux.HandleFunc("GET /sessions", h.page(h.sessionList))
	mux.HandleFunc("GET /sessions/{id}", h.page(h.sessionDetail))
	mux.HandleFunc("GET /models", h.page(h.models))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h.renderError(w, r, http.StatusNotFound, "Page not found.")
	})
	return mux
}

func cacheFor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		next.ServeHTTP(w, r)
	})
}

// page wraps a page handler with the session requirement.
func (h *Handler) page(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.opts.RequireLogin && !h.SessionAuth(r) {
			if isHTMX(r) {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("Vary", "HX-Request")
		next(w, r)
	}
}

// SessionAuth reports whether r carries a valid dashboard session cookie. It
// is meant for httpapi.Options.SessionAuth, which applies it to GET and HEAD
// requests of /v1/* only.
func (h *Handler) SessionAuth(r *http.Request) bool {
	c, err := r.Cookie(SessionCookie)
	return err == nil && c.Value != "" && h.sessions.has(c.Value)
}

// isHTMX reports whether r is an htmx request that wants a fragment (a history
// restore after a cache miss wants the whole page).
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == trueValue && r.Header.Get("HX-History-Restore-Request") != trueValue
}

// randSource serialises reads of an injected, possibly non-thread-safe reader.
type randSource struct {
	mu sync.Mutex
	r  io.Reader
}

func (s *randSource) read(n int) ([]byte, error) {
	b := make([]byte, n)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := io.ReadFull(s.r, b); err != nil {
		return nil, fmt.Errorf("web: read randomness: %w", err)
	}
	return b, nil
}

// view names a page template file and the template in it to execute.
type view struct{ page, name string }

// render executes the named page template into a buffer and writes it, so a
// template failure still yields a clean 500.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, v view, data any) {
	page, name := v.page, v.name
	t, ok := h.pages[page]
	if !ok {
		h.log.Error("unknown template", "component", "web", "path", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		h.log.Error("render", "component", "web", "operation", page, "error", err.Error(),
			"request_id", httpmw.RequestIDFrom(r.Context()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// renderPage renders a full page whose entry template is "layout".
func (h *Handler) renderPage(w http.ResponseWriter, r *http.Request, page string, data any) {
	h.render(w, r, http.StatusOK, view{page, layoutTemplate}, data)
}

// errorData is the data of the error page.
type errorData struct {
	base
	Status  int
	Message string
}

func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	b := h.base(r, http.StatusText(status), "")
	h.render(w, r, status, view{"error.html", layoutTemplate}, errorData{base: b, Status: status, Message: msg})
}

// fail maps an error of the query layer to an error page.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case isNotFound(err):
		h.renderError(w, r, http.StatusNotFound, "Not found.")
	case isInvalid(err):
		h.renderError(w, r, http.StatusBadRequest, err.Error())
	default:
		h.log.Error("query failed", "component", "web", "operation", r.URL.Path, "error", err.Error(),
			"request_id", httpmw.RequestIDFrom(r.Context()))
		h.renderError(w, r, http.StatusInternalServerError, "Internal error.")
	}
}
