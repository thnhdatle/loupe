// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/web"
	assets "github.com/thnhdatle/loupe/web"
)

var (
	nonceRe  = regexp.MustCompile(`'nonce-([A-Za-z0-9+/]+)'`)
	scriptRe = regexp.MustCompile(`(?is)<script\b([^>]*)>`)
	styleRe  = regexp.MustCompile(`(?is)(<style\b|\sstyle\s*=)`)
	onAttrRe = regexp.MustCompile(`(?is)<[a-z][^>]*\son[a-z]+\s*=`)
)

func wantCSP(nonce string) string {
	return "default-src 'self'; script-src 'self' 'nonce-" + nonce + "'; style-src 'self'; img-src 'self' data:; " +
		"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
}

// pageURLs lists a URL for every page and fragment the dashboard renders.
var pageURLs = []string{
	"/", "/?range=30d", "/traces", "/traces/" + traceID, "/traces/" + traceID + "?span=a000000000000002",
	"/traces/" + traceID + "?span=a000000000000003", "/sessions", "/sessions/sess-1", "/models", "/login", "/nope",
}

func TestSecurity_CSPHeader(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	seen := map[string]bool{}
	for _, u := range pageURLs {
		rec := e.get(u)
		csp := rec.Header().Get("Content-Security-Policy")
		m := nonceRe.FindStringSubmatch(csp)
		require.Len(t, m, 2, u)
		assert.Equal(t, wantCSP(m[1]), csp, u)
		assert.False(t, seen[m[1]], "nonce reused: %s", u)
		seen[m[1]] = true
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), u)
		assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"), u)
	}
	rec := e.get("/static/app.js")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
}

func TestSecurity_NoInlineStylesOrUnnoncedScripts(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	for _, u := range pageURLs {
		rec := e.get(u)
		b := body(rec)
		nonce := nonceRe.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))[1]
		assert.False(t, styleRe.MatchString(b), "inline style in %s", u)
		assert.False(t, onAttrRe.MatchString(b), "inline event handler in %s", u)
		for _, m := range scriptRe.FindAllStringSubmatch(b, -1) {
			attrs := m[1]
			assert.True(t, strings.Contains(attrs, ` src="/static/`) || strings.Contains(attrs, `nonce="`+nonce+`"`),
				"script without src or the response nonce in %s: <script%s>", u, attrs)
		}
		assert.NotContains(t, b, "javascript:", u)
		assert.NotContains(t, b, "https://", u+": no external resources")
		assert.NotContains(t, b, "http://", u+": no external resources")
	}
}

func TestSecurity_TemplateSourcesHaveNoInlineStyle(t *testing.T) {
	t.Parallel()
	files, err := fs.Glob(assets.FS, "templates/*.html")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		raw, err := fs.ReadFile(assets.FS, f)
		require.NoError(t, err)
		assert.False(t, styleRe.Match(raw), "inline style in %s", f)
		assert.False(t, onAttrRe.Match(raw), "inline event handler in %s", f)
		for _, m := range scriptRe.FindAllSubmatch(raw, -1) {
			attrs := string(m[1])
			assert.True(t, strings.Contains(attrs, "src=") || strings.Contains(attrs, "nonce="),
				"script without src or nonce in %s: <script%s>", f, attrs)
		}
	}
}

func TestSecurity_HTMXConfig(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	b := body(e.get("/"))
	assert.Contains(t, b, `<meta name="htmx-config" content='`+
		`{"includeIndicatorStyles":false,"allowEval":false,"allowScriptTags":false}'>`)
	assert.Contains(t, b, `<link rel="icon" href="data:,">`)
}

func TestSecurity_ContentIsEscaped(t *testing.T) {
	t.Parallel()
	e := newEnv(t, web.Options{})
	e.q.tracePage.Data[0].RootName = `<img src=x onerror=alert(1)>`
	b := body(e.get("/traces"))
	assert.NotContains(t, b, "<img src=x")
	assert.Contains(t, b, "&lt;img src=x onerror=alert(1)&gt;")
}

func TestSecurity_AssetsAreVendored(t *testing.T) {
	t.Parallel()
	for _, f := range []string{
		"static/vendor/htmx.min.js", "static/vendor/uPlot.iife.min.js", "static/vendor/uPlot.min.css",
		"static/vendor/LICENSE-htmx.txt", "static/vendor/LICENSE-uplot.txt", "static/app.js", "static/app.css",
	} {
		info, err := fs.Stat(assets.FS, f)
		require.NoError(t, err, f)
		assert.Positive(t, info.Size(), f)
	}
	raw, err := fs.ReadFile(assets.FS, "static/app.js")
	require.NoError(t, err)
	assert.NotRegexp(t, `\beval\s*\(|new Function|https?://`, string(raw))
	css, err := fs.ReadFile(assets.FS, "static/app.css")
	require.NoError(t, err)
	assert.NotRegexp(t, `https?://|@import|url\(`, string(css))
}
