// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Defaults for the receiver limits.
const (
	DefaultMaxRequestBytes    = 16 << 20
	DefaultMaxSpansPerRequest = 10_000
	DefaultShutdownTimeout    = 30 * time.Second
)

// ErrInvalidConfig reports an unusable Config or missing dependency.
var ErrInvalidConfig = errors.New("otlp: invalid config")

// Config holds the receiver settings. The zero value is invalid; start from DefaultConfig.
type Config struct {
	// MaxRequestBytes caps one request after decompression (receiver.max_request_bytes).
	MaxRequestBytes int64
	// MaxSpansPerRequest caps the spans of one traces request (receiver.max_spans_per_request).
	MaxSpansPerRequest int
	// Tokens are the accepted bearer tokens. Empty disables authentication; the caller
	// decides whether that is allowed for the listener (receiver.allow_unauthenticated).
	Tokens []string
	// ShutdownTimeout bounds the drain of in-flight requests in RunHTTP and RunGRPC.
	ShutdownTimeout time.Duration
	// HTTPTLS and GRPCTLS enable TLS on the respective listener when non-nil.
	HTTPTLS *tls.Config
	GRPCTLS *tls.Config
	// Logger receives warnings; nil discards them.
	Logger *slog.Logger
}

// DefaultConfig returns the documented defaults.
func DefaultConfig() Config {
	return Config{
		MaxRequestBytes:    DefaultMaxRequestBytes,
		MaxSpansPerRequest: DefaultMaxSpansPerRequest,
		ShutdownTimeout:    DefaultShutdownTimeout,
	}
}

func (c Config) validate() error {
	var errs []error
	if c.MaxRequestBytes <= 0 {
		errs = append(errs, fmt.Errorf("%w: max request bytes must be positive", ErrInvalidConfig))
	}
	if c.MaxSpansPerRequest <= 0 {
		errs = append(errs, fmt.Errorf("%w: max spans per request must be positive", ErrInvalidConfig))
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("%w: shutdown timeout must be positive", ErrInvalidConfig))
	}
	for _, t := range c.Tokens {
		if t == "" {
			errs = append(errs, fmt.Errorf("%w: empty token", ErrInvalidConfig))
			break
		}
	}
	return errors.Join(errs...)
}
