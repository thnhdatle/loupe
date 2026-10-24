// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"io"
	"net/url"

	"gopkg.in/yaml.v3"
)

const Redacted = "***"

func (c *Config) Redact() Config {
	out := *c
	out.Content.Redact = append([]RedactRule(nil), c.Content.Redact...)
	mask := func(s *string) {
		if *s != "" {
			*s = Redacted
		}
	}
	mask(&out.Receiver.TokensFile)
	mask(&out.API.TokenFile)
	mask(&out.Receiver.GRPC.TLS.KeyFile)
	mask(&out.Receiver.HTTP.TLS.KeyFile)
	mask(&out.UI.TLS.KeyFile)
	mask(&out.Analyze.APIKeyFile)
	out.SelfTracing.Endpoint = redactEndpoint(c.SelfTracing.Endpoint)
	out.Analyze.BaseURL = redactEndpoint(c.Analyze.BaseURL)
	return out
}

func redactEndpoint(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Redacted
	}
	if u.User != nil {
		u.User = url.User(Redacted)
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

func (c *Config) WriteYAML(w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	return nil
}
