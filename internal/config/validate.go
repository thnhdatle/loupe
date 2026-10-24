// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/content"
)

var ErrInvalid = errors.New("invalid configuration")

type Error struct {
	errs []error
}

func (e *Error) Error() string {
	lines := make([]string, 0, len(e.errs))
	for _, err := range e.errs {
		lines = append(lines, "  - "+err.Error())
	}
	return fmt.Sprintf("invalid configuration (%d problems):\n%s", len(e.errs), strings.Join(lines, "\n"))
}

func (e *Error) Unwrap() []error { return append([]error{ErrInvalid}, e.errs...) }

func (e *Error) Problems() []error { return append([]error(nil), e.errs...) }

type validator struct{ errs []error }

func (v *validator) bad(format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf(format, args...))
}

// Validate reports every problem in the configuration at once.
func (c *Config) Validate() error {
	v := &validator{}
	v.listeners(c)
	v.auth(c)
	v.tls(c)
	v.limits(c)
	v.enums(c)
	v.redact(c)
	v.analyze(c)
	if err := c.RetentionConfig().Validate(); err != nil {
		if multi, ok := err.(interface{ Unwrap() []error }); ok {
			v.errs = append(v.errs, multi.Unwrap()...)
		} else {
			v.errs = append(v.errs, err)
		}
	}
	if len(v.errs) == 0 {
		return nil
	}
	return &Error{errs: v.errs}
}

type bound struct {
	name   string
	listen string
	host   string
	port   int
}

func (v *validator) listeners(c *Config) {
	var bounds []bound
	for _, l := range []struct{ name, addr string }{
		{"receiver.grpc.listen", c.Receiver.GRPC.Listen},
		{"receiver.http.listen", c.Receiver.HTTP.Listen},
		{"ui.listen", c.UI.Listen},
		{"admin.listen", c.Admin.Listen},
	} {
		host, port, err := splitListen(l.addr)
		if err != nil {
			v.bad("%s %q: %v", l.name, l.addr, err)
			continue
		}
		bounds = append(bounds, bound{l.name, l.addr, host, port})
	}
	for i, a := range bounds {
		for _, b := range bounds[i+1:] {
			if a.port != 0 && a.port == b.port && (a.host == b.host || isWildcard(a.host) || isWildcard(b.host)) {
				v.bad("%s (%s) and %s (%s) must not collide", a.name, a.listen, b.name, b.listen)
			}
		}
	}
}

func (v *validator) auth(c *Config) {
	for _, l := range []struct{ name, addr string }{
		{"receiver.grpc.listen", c.Receiver.GRPC.Listen},
		{"receiver.http.listen", c.Receiver.HTTP.Listen},
	} {
		if nonLoopback(l.addr) && c.Receiver.TokensFile == "" && !c.Receiver.AllowUnauthenticated {
			v.bad("%s %q is not loopback: set receiver.tokens_file or receiver.allow_unauthenticated: true",
				l.name, l.addr)
		}
	}
	if nonLoopback(c.UI.Listen) && c.API.TokenFile == "" && !c.UI.AllowUnauthenticated {
		v.bad("ui.listen %q is not loopback: set api.token_file or ui.allow_unauthenticated: true", c.UI.Listen)
	}
	if c.API.GenerateToken && c.API.TokenFile == "" {
		v.bad("api.generate_token requires api.token_file")
	}
}

func (v *validator) tls(c *Config) {
	for _, t := range []struct {
		name string
		tls  TLS
	}{
		{"receiver.grpc.tls", c.Receiver.GRPC.TLS},
		{"receiver.http.tls", c.Receiver.HTTP.TLS},
		{"ui.tls", c.UI.TLS},
	} {
		if (t.tls.CertFile == "") != (t.tls.KeyFile == "") {
			v.bad("%s: cert_file and key_file must both be set or both empty", t.name)
		}
	}
}

func (v *validator) limits(c *Config) {
	for _, p := range []struct {
		name string
		val  int64
	}{
		{"receiver.max_request_bytes", c.Receiver.MaxRequestBytes.Bytes()},
		{"receiver.max_spans_per_request", int64(c.Receiver.MaxSpansPerRequest)},
		{"receiver.max_attribute_bytes", c.Receiver.MaxAttributeBytes.Bytes()},
		{"receiver.max_inflight_bytes", c.Receiver.MaxInflightBytes.Bytes()},
		{"ui.session_ttl", int64(c.UI.SessionTTL)},
		{"ingest.queue_spans", int64(c.Ingest.QueueSpans)},
		{"ingest.queue_bytes", c.Ingest.QueueBytes.Bytes()},
		{"writer.batch_spans", int64(c.Writer.BatchSpans)},
		{"writer.batch_interval", int64(c.Writer.BatchInterval)},
		{"content.max_bytes", c.Content.MaxBytes.Bytes()},
		{"analyze.timeout", int64(c.Analyze.Timeout)},
		{"analyze.max_spans", int64(c.Analyze.MaxSpans)},
		{"shutdown_timeout", int64(c.ShutdownTimeout)},
	} {
		if p.val <= 0 {
			v.bad("%s must be positive", p.name)
		}
	}
	if c.Runtime.MemoryLimit < 0 {
		v.bad("runtime.memory_limit must not be negative")
	}
	if c.Storage.Path == "" {
		v.bad("storage.path must not be empty")
	}
}

func (v *validator) enums(c *Config) {
	oneOf := func(name, val string, allowed ...string) {
		for _, a := range allowed {
			if val == a {
				return
			}
		}
		v.bad("%s %q must be one of %s", name, val, strings.Join(allowed, ", "))
	}
	oneOf("ingest.overflow", c.Ingest.Overflow, "reject", "drop")
	oneOf("content.capture", c.Content.Capture,
		string(content.ModeOff), string(content.ModeOn), string(content.ModeMetadataOnly))
	oneOf("log.level", c.Log.Level, "debug", "info", "warn", "error")
	oneOf("log.format", c.Log.Format, "json", "text")
}

func (v *validator) redact(c *Config) {
	if _, err := content.NewRedactor(c.Content.RedactPresets(), c.Content.RedactRules()); err != nil {
		if multi, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range multi.Unwrap() {
				v.bad("content.redact: %v", e)
			}
		} else {
			v.bad("content.redact: %v", err)
		}
	}
	seen := map[string]bool{}
	for _, r := range c.Content.RedactRules() {
		if seen[r.Name] && r.Name != "" {
			v.bad("content.redact: duplicate rule name %q", r.Name)
		}
		seen[r.Name] = true
	}
}

func (v *validator) analyze(c *Config) {
	a := c.Analyze
	if !a.Enabled() {
		return
	}
	if u, err := url.Parse(a.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		v.bad("analyze.base_url %q must be an http or https URL", a.BaseURL)
	} else if u.User != nil {
		v.bad("analyze.base_url must not contain credentials; use analyze.api_key_file")
	}
	if a.Model == "" {
		v.bad("analyze.model is required when analyze.base_url is set")
	}
}

func (c *Config) Warnings() []string {
	var out []string
	for _, l := range []struct {
		name string
		addr string
		tls  TLS
	}{
		{"receiver.grpc", c.Receiver.GRPC.Listen, c.Receiver.GRPC.TLS},
		{"receiver.http", c.Receiver.HTTP.Listen, c.Receiver.HTTP.TLS},
		{"ui", c.UI.Listen, c.UI.TLS},
	} {
		if nonLoopback(l.addr) && !l.tls.Enabled() {
			out = append(out, fmt.Sprintf("%s.listen %q is not loopback and has no TLS; use a TLS-terminating reverse proxy",
				l.name, l.addr))
		}
	}
	if c.Analyze.Enabled() && c.Analyze.IncludeContent && !c.AnalyzeContent() {
		out = append(out, "analyze.include_content needs content.capture: on; no content is sent")
	}
	if c.SelfTracing.Endpoint != "" {
		out = append(out, "self_tracing.endpoint is set but self-tracing is not implemented yet; the value is ignored")
	}
	return out
}

func splitListen(addr string) (string, int, error) {
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, errors.New("want host:port")
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port < 0 || port > 65535 {
		return "", 0, errors.New("invalid port")
	}
	return strings.ToLower(host), port, nil
}

func isWildcard(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func nonLoopback(addr string) bool {
	host, _, err := splitListen(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback()
	}
	return true
}
