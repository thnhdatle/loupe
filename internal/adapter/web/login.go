// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	LoginCodeTTL = 15 * time.Minute
	maxLoginBody = 4 << 10
	secretBytes  = 32
)

type loginData struct {
	base
	Message string
}

type base struct {
	Title    string
	Nav      string
	Nonce    string
	LoggedIn bool
}

func (h *Handler) base(r *http.Request, title, nav string) base {
	return base{Title: title, Nav: nav, Nonce: nonceOf(r.Context()), LoggedIn: h.SessionAuth(r)}
}

func (h *Handler) NewLoginCode() (string, error) {
	raw, err := h.rnd.read(secretBytes)
	if err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	h.codes.add(code, LoginCodeTTL)
	return code, nil
}

func LoginURL(listen string, tls bool, code string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		host, port = listen, ""
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	scheme := "http"
	if tls {
		scheme = "https"
	}
	hostport := host
	if port != "" {
		hostport = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		hostport = "[" + host + "]"
	}
	return scheme + "://" + hostport + "/login?code=" + code
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if code := r.URL.Query().Get("code"); code != "" {
		h.codeLogin(w, r, code)
		return
	}
	if !h.opts.RequireLogin || h.SessionAuth(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.renderLogin(w, r, http.StatusOK, "")
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, status int, msg string) {
	h.render(w, r, status, view{"login.html", layoutTemplate}, loginData{base: h.base(r, "Sign in", ""), Message: msg})
}

func (h *Handler) throttled(w http.ResponseWriter, r *http.Request) bool {
	left, blocked := h.limiter.blocked(clientIP(r))
	if !blocked {
		return false
	}
	secs := int((left + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	h.renderError(w, r, http.StatusTooManyRequests, "Too many failed sign-in attempts. Try again in a minute.")
	return true
}

func (h *Handler) codeLogin(w http.ResponseWriter, r *http.Request, code string) {
	if h.throttled(w, r) {
		return
	}
	if !h.codes.take(code) {
		h.limiter.fail(clientIP(r))
		h.renderLogin(w, r, http.StatusUnauthorized, "The login code is invalid, expired or already used.")
		return
	}
	h.startSession(w, r)
}

func (h *Handler) loginSubmit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r) {
		h.renderError(w, r, http.StatusForbidden, "Cross-origin request rejected.")
		return
	}
	if h.throttled(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "Malformed form.")
		return
	}
	if !h.bearer.Check("Bearer " + r.PostForm.Get("token")) {
		h.limiter.fail(clientIP(r))
		h.renderLogin(w, r, http.StatusUnauthorized, "The token is not valid.")
		return
	}
	h.startSession(w, r)
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request) {
	raw, err := h.rnd.read(secretBytes)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	exp := h.sessions.add(id, h.ttl)
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure follows ui.tls/ui.secure_cookie.
		Name: SessionCookie, Value: id, Path: "/", Expires: exp, MaxAge: int(h.ttl / time.Second),
		HttpOnly: true, Secure: h.opts.SecureCookie, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		h.renderError(w, r, http.StatusForbidden, "Cross-origin request rejected.")
		return
	}
	if c, err := r.Cookie(SessionCookie); err == nil {
		h.sessions.remove(c.Value)
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure follows ui.tls/ui.secure_cookie.
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: h.opts.SecureCookie, SameSite: http.SameSiteStrictMode,
	})
	next := "/"
	if h.opts.RequireLogin {
		next = "/login"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}
