// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/web"
)

func TestAuth_PagesRedirectWithoutSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	for _, p := range []string{"/", "/traces", "/traces/" + traceID, "/sessions", "/sessions/x", "/models"} {
		rec := e.get(p)
		assert.Equal(t, http.StatusSeeOther, rec.Code, p)
		assert.Equal(t, "/login", rec.Header().Get("Location"), p)
	}
	rec := e.get("/traces", withHeader("HX-Request", "true"))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("HX-Redirect"))

	rec = e.get("/login")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body(rec), `<form method="post" action="/login"`)
	assert.Contains(t, body(rec), `type="password" name="token"`)
}

func TestAuth_LoginLogout(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})

	rec := e.post("/login", url.Values{"token": {"wrong"}})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, body(rec), "The token is not valid.")
	assert.Empty(t, rec.Result().Cookies())
	assert.Equal(t, http.StatusUnauthorized, e.post("/login", url.Values{}).Code)

	c := e.login(t)
	assert.Equal(t, web.SessionCookie, c.Name)
	assert.Equal(t, "/", c.Path)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.False(t, c.Secure)
	assert.Equal(t, int(web.DefaultSessionTTL.Seconds()), c.MaxAge)
	assert.True(t, c.Expires.Equal(epoch.Add(web.DefaultSessionTTL)))
	assert.NotContains(t, c.Value, testToken)

	rec = e.get("/", withCookie(c))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body(rec), "Sign out")
	assert.Equal(t, http.StatusSeeOther, e.get("/login", withCookie(c)).Code, "signed in: login redirects home")

	rec = e.post("/logout", url.Values{}, withCookie(c))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
	cleared := sessionCookie(t, rec)
	assert.Equal(t, -1, cleared.MaxAge)
	assert.Equal(t, http.StatusSeeOther, e.get("/", withCookie(c)).Code, "the session is gone")
}

func TestAuth_SessionExpiry(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true, SessionTTL: time.Hour})
	c := e.login(t)
	assert.Equal(t, 3600, c.MaxAge)
	probe := func() bool {
		return e.h.SessionAuth(newGet("/v1/traces", c))
	}
	assert.True(t, probe())
	e.clk.Advance(59 * time.Minute)
	assert.True(t, probe())
	e.clk.Advance(time.Minute)
	assert.False(t, probe(), "expired at ttl")
	assert.Equal(t, http.StatusSeeOther, e.get("/", withCookie(c)).Code)
}

func TestAuth_SessionAuth(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	c := e.login(t)
	assert.True(t, e.h.SessionAuth(newGet("/v1/x", c)))
	assert.False(t, e.h.SessionAuth(newGet("/v1/x", nil)))
	assert.False(t, e.h.SessionAuth(newGet("/v1/x", &http.Cookie{Name: web.SessionCookie, Value: "forged"})))
	assert.False(t, e.h.SessionAuth(newGet("/v1/x", &http.Cookie{Name: web.SessionCookie, Value: ""})))
	assert.False(t, e.h.SessionAuth(newGet("/v1/x", &http.Cookie{Name: "other", Value: c.Value})))
}

func TestAuth_CookieFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		secure bool
	}{{"plain http", false}, {"tls or secure_cookie", true}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, web.Options{RequireLogin: true, SecureCookie: tc.secure})
			c := e.login(t)
			assert.Equal(t, tc.secure, c.Secure)
			assert.True(t, c.HttpOnly)
			assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
			rec := e.post("/logout", url.Values{}, withCookie(c))
			assert.Equal(t, tc.secure, sessionCookie(t, rec).Secure)
		})
	}
}

func TestAuth_RateLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	bad := url.Values{"token": {"nope"}}
	for i := range 5 {
		assert.Equal(t, http.StatusUnauthorized, e.post("/login", bad).Code, "attempt %d", i+1)
		e.clk.Advance(5 * time.Second)
	}
	rec := e.post("/login", url.Values{"token": {testToken}})
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "35", rec.Header().Get("Retry-After"))
	assert.Empty(t, rec.Result().Cookies())
	assert.Equal(t, http.StatusSeeOther,
		e.post("/login", url.Values{"token": {testToken}}, withAddr("198.51.100.7:4000")).Code)
	assert.Equal(t, http.StatusTooManyRequests,
		e.post("/login", url.Values{"token": {testToken}}, withAddr("192.0.2.1:9999")).Code)

	e.clk.Advance(34 * time.Second)
	assert.Equal(t, http.StatusTooManyRequests, e.post("/login", url.Values{"token": {testToken}}).Code)
	e.clk.Advance(time.Second)
	assert.Equal(t, http.StatusSeeOther, e.post("/login", url.Values{"token": {testToken}}).Code, "window over")
}

func TestAuth_RateLimitWindowResets(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	bad := url.Values{"token": {"nope"}}
	for range 4 {
		e.post("/login", bad)
	}
	e.clk.Advance(61 * time.Second)
	for i := range 4 {
		assert.Equal(t, http.StatusUnauthorized, e.post("/login", bad).Code, "fresh window, attempt %d", i+1)
	}
	assert.Equal(t, http.StatusSeeOther, e.post("/login", url.Values{"token": {testToken}}).Code)
}

func TestAuth_SuccessDoesNotCountAsFailure(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	for range 10 {
		assert.Equal(t, http.StatusSeeOther, e.post("/login", url.Values{"token": {testToken}}).Code)
	}
}

func TestAuth_OriginCheck(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	form := url.Values{"token": {testToken}}
	for _, origin := range []string{"https://evil.example", "null", "http://example.com:81", "::bad"} {
		rec := e.post("/login", form, withHeader("Origin", origin))
		assert.Equal(t, http.StatusForbidden, rec.Code, origin)
		assert.Empty(t, rec.Result().Cookies(), origin)
	}
	assert.Equal(t, http.StatusForbidden,
		e.post("/login", form, withHeader("Origin", "null"), withHeader("Sec-Fetch-Site", "cross-site")).Code)
	assert.Equal(t, http.StatusSeeOther,
		e.post("/login", form, withHeader("Origin", "null"), withHeader("Sec-Fetch-Site", "same-origin")).Code)
	c := e.login(t)
	assert.Equal(t, http.StatusForbidden,
		e.post("/logout", url.Values{}, withCookie(c), withHeader("Origin", "https://evil.example")).Code)
	assert.True(t, e.h.SessionAuth(newGet("/", c)), "a rejected logout keeps the session")

	assert.Equal(t, http.StatusSeeOther, e.post("/login", form, withHeader("Origin", "http://example.com")).Code)
	assert.Equal(t, http.StatusSeeOther,
		e.post("/logout", url.Values{}, withCookie(c), withHeader("Origin", "http://EXAMPLE.com")).Code)
}

func TestAuth_LoginBodyLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	big := make([]byte, 64<<10)
	for i := range big {
		big[i] = 'a'
	}
	rec := e.post("/login", url.Values{"token": {string(big)}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuth_NoLoginRequired(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	assert.Equal(t, http.StatusOK, e.get("/").Code)
	rec := e.get("/login")
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
	assert.NotContains(t, body(e.get("/")), "Sign out")

	c := e.login(t)
	assert.Contains(t, body(e.get("/", withCookie(c))), "Sign out")
	rec = e.post("/logout", url.Values{}, withCookie(c))
	assert.Equal(t, "/", rec.Header().Get("Location"))
}

func TestAuth_NoTokensNoLogin(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true, Tokens: []string{}})
	assert.Equal(t, http.StatusUnauthorized, e.post("/login", url.Values{"token": {""}}).Code)
	assert.Equal(t, http.StatusUnauthorized, e.post("/login", url.Values{"token": {"x"}}).Code)
}

func TestAuth_CodeLogin(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true, SecureCookie: true})
	code, err := e.h.NewLoginCode()
	require.NoError(t, err)
	require.Len(t, code, 43)

	rec := e.get("/login?code=" + code)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
	c := sessionCookie(t, rec)
	assert.True(t, c.HttpOnly && c.Secure)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.Equal(t, http.StatusOK, e.get("/", withCookie(c)).Code)

	rec = e.get("/login?code=" + code)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "a code is single-use")
	assert.Empty(t, rec.Result().Cookies())
	assert.Contains(t, body(rec), "invalid, expired or already used")
}

func TestAuth_CodeExpiry(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	code, err := e.h.NewLoginCode()
	require.NoError(t, err)
	e.clk.Advance(web.LoginCodeTTL - time.Second)
	other, err := e.h.NewLoginCode()
	require.NoError(t, err)
	assert.NotEqual(t, code, other)
	e.clk.Advance(time.Second)
	assert.Equal(t, http.StatusUnauthorized, e.get("/login?code="+code).Code, "expired after 15 min")
	assert.Equal(t, http.StatusSeeOther, e.get("/login?code="+other).Code, "newer code still valid")
}

func TestAuth_CodeAttemptsCountTowardLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	code, err := e.h.NewLoginCode()
	require.NoError(t, err)
	for range 5 {
		assert.Equal(t, http.StatusUnauthorized, e.get("/login?code=wrong").Code)
	}
	assert.Equal(t, http.StatusTooManyRequests, e.get("/login?code="+code).Code, "blocked, even with a valid code")
	e.clk.Advance(time.Minute)
	assert.Equal(t, http.StatusSeeOther, e.get("/login?code="+code).Code)

	for range 3 {
		e.post("/login", url.Values{"token": {"x"}})
	}
	e.get("/login?code=a")
	e.get("/login?code=b")
	assert.Equal(t, http.StatusTooManyRequests, e.post("/login", url.Values{"token": {testToken}}).Code)
}

func TestLoginURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		listen string
		tls    bool
		want   string
	}{
		{"loopback", "127.0.0.1:3000", false, "http://127.0.0.1:3000/login?code=C"},
		{"unspecified v4", "0.0.0.0:3000", false, "http://localhost:3000/login?code=C"},
		{"unspecified v6", "[::]:3000", false, "http://localhost:3000/login?code=C"},
		{"empty host", ":3000", false, "http://localhost:3000/login?code=C"},
		{"named host", "loupe.example:8080", false, "http://loupe.example:8080/login?code=C"},
		{"v6 literal", "[2001:db8::1]:3000", false, "http://[2001:db8::1]:3000/login?code=C"},
		{"tls", "0.0.0.0:443", true, "https://localhost:443/login?code=C"},
		{"no port", "example.com", false, "http://example.com/login?code=C"},
		{"no port v6", "2001:db8::1", false, "http://[2001:db8::1]/login?code=C"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, web.LoginURL(tc.listen, tc.tls, "C"))
		})
	}
}

func TestNewHandler_Invalid(t *testing.T) {
	t.Parallel()
	_, err := web.NewHandler(nil, web.Options{})
	require.ErrorIs(t, err, web.ErrInvalidOptions)
	_, err = web.NewHandler(fixtures(), web.Options{SessionTTL: -time.Second})
	require.ErrorIs(t, err, web.ErrInvalidOptions)
	h, err := web.NewHandler(fixtures(), web.Options{})
	require.NoError(t, err)
	code, err := h.NewLoginCode()
	require.NoError(t, err)
	assert.NotEmpty(t, code, "defaults: system clock and crypto/rand")
}

func TestAuth_Retry_AfterIsInteger(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{RequireLogin: true})
	for range 5 {
		e.post("/login", url.Values{"token": {"x"}})
	}
	rec := e.post("/login", url.Values{"token": {"x"}})
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	require.NoError(t, err)
	assert.Equal(t, 60, secs)
}

func newGet(target string, c *http.Cookie) *http.Request {
	r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
	if c != nil {
		r.AddCookie(c)
	}
	return r
}
