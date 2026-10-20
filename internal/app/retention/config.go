// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"errors"
	"fmt"
	"time"
)

// Defaults of the retention section.
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

// MinMaxDBBytes is the smallest non-zero retention.max_db_bytes.
const MinMaxDBBytes = int64(64) << 20

// CapFloor is the age below which the disk cap never deletes spans or content:
// the last hour of data always survives, even above the cap.
const CapFloor = time.Hour

// ErrInvalidConfig reports a retention configuration that fails validation.
var ErrInvalidConfig = errors.New("retention: invalid config")

// Config is the retention section. Durations are ages relative to the clock's
// now; MaxDBBytes 0 turns the disk cap off.
type Config struct {
	Spans         time.Duration // retention.spans
	Content       time.Duration // retention.content, ≤ Spans
	RollupsHourly time.Duration // retention.rollups_hourly (rollup_hour, rollup_user_hour), ≤ RollupsDaily
	RollupsDaily  time.Duration // retention.rollups_daily (rollup_day, rollup_user_day, ingest_stats)
	OrphanContent time.Duration // retention.orphan_content: content rows without a stored span
	MaxDBBytes    int64         // retention.max_db_bytes: main + WAL + SHM; 0 = off, else ≥ MinMaxDBBytes
	Interval      time.Duration // retention.interval between passes
	Batch         int           // retention.batch: rows per writer job
}

// DefaultConfig returns the documented defaults.
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

// Validate reports every problem at once, each wrapping ErrInvalidConfig.
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
