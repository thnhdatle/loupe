// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"errors"
	"fmt"
	"time"
)

const (
	DefaultSpans         = 14 * 24 * time.Hour
	DefaultContent       = 7 * 24 * time.Hour
	DefaultRollupsHourly = 35 * 24 * time.Hour
	DefaultRollupsDaily  = 400 * 24 * time.Hour
	DefaultOrphanContent = time.Hour
	DefaultMaxDBBytes    = int64(10) << 30
	DefaultInterval      = 10 * time.Minute
	DefaultBatch         = 5000
)

const MinMaxDBBytes = int64(64) << 20

const CapFloor = time.Hour

var ErrInvalidConfig = errors.New("retention: invalid config")

type Config struct {
	Spans         time.Duration
	Content       time.Duration
	RollupsHourly time.Duration
	RollupsDaily  time.Duration
	OrphanContent time.Duration
	MaxDBBytes    int64
	Interval      time.Duration
	Batch         int
}

func DefaultConfig() Config {
	return Config{
		Spans:         DefaultSpans,
		Content:       DefaultContent,
		RollupsHourly: DefaultRollupsHourly,
		RollupsDaily:  DefaultRollupsDaily,
		OrphanContent: DefaultOrphanContent,
		MaxDBBytes:    DefaultMaxDBBytes,
		Interval:      DefaultInterval,
		Batch:         DefaultBatch,
	}
}

func (c Config) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{ErrInvalidConfig}, args...)...))
	}
	for _, d := range []struct {
		name string
		v    time.Duration
	}{
		{"spans", c.Spans},
		{"content", c.Content},
		{"rollups_hourly", c.RollupsHourly},
		{"rollups_daily", c.RollupsDaily},
		{"orphan_content", c.OrphanContent},
		{"interval", c.Interval},
	} {
		if d.v <= 0 {
			bad("retention.%s must be positive, got %s", d.name, d.v)
		}
	}
	if c.Content > c.Spans {
		bad("retention.content (%s) must not exceed retention.spans (%s)", c.Content, c.Spans)
	}
	if c.RollupsHourly > c.RollupsDaily {
		bad("retention.rollups_hourly (%s) must not exceed retention.rollups_daily (%s)",
			c.RollupsHourly, c.RollupsDaily)
	}
	if c.MaxDBBytes != 0 && c.MaxDBBytes < MinMaxDBBytes {
		bad("retention.max_db_bytes must be 0 or at least %d, got %d", MinMaxDBBytes, c.MaxDBBytes)
	}
	if c.Batch < 1 {
		bad("retention.batch must be at least 1, got %d", c.Batch)
	}
	return errors.Join(errs...)
}
