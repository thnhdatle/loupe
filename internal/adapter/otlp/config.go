// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	DefaultMaxRequestBytes    = 16 << 20
	DefaultMaxSpansPerRequest = 10_000
	DefaultShutdownTimeout    = 30 * time.Second
)

var ErrInvalidConfig = errors.New("otlp: invalid config")

// Config holds the receiver settings. Start from DefaultConfig.
type Config struct {
	MaxRequestBytes    int64
	MaxSpansPerRequest int
	Tokens             []string
	ShutdownTimeout    time.Duration
	HTTPTLS            *tls.Config
	GRPCTLS            *tls.Config
	Logger             *slog.Logger
}

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
