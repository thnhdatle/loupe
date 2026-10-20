// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Resolution is the time bucket of a rollup table.
type Resolution uint8

// Rollup resolutions: rollup_hour / rollup_user_hour and rollup_day / rollup_user_day.
const (
	ResolutionHour Resolution = iota + 1
	ResolutionDay
)

// Nanoseconds per bucket.
const (
	HourNs = int64(time.Hour)
	DayNs  = 24 * HourNs
)

// Duration returns the bucket length in nanoseconds.
func (r Resolution) Duration() int64 {
	if r == ResolutionDay {
		return DayNs
	}
	return HourNs
}

// Start returns the UTC start of the bucket containing ns (Unix nanoseconds).
func (r Resolution) Start(ns int64) int64 { return floorTo(ns, r.Duration()) }

// String returns "hour" or "day".
func (r Resolution) String() string {
	if r == ResolutionDay {
		return "day"
	}
	return "hour"
}

// HourStart returns the start of the UTC hour containing ns.
func HourStart(ns int64) int64 { return floorTo(ns, HourNs) }

// DayStart returns the start of the UTC day containing ns.
func DayStart(ns int64) int64 { return floorTo(ns, DayNs) }

// floorTo rounds ns down to a multiple of step, also for negative ns.
func floorTo(ns, step int64) int64 {
	r := ns % step
	if r < 0 {
		r += step
	}
	return ns - r
}

// Key identifies a row of rollup_hour or rollup_day.
type Key struct {
	BucketNs int64 // UTC start of the hour or day, Unix nanoseconds
	Service  string
	Provider string
	Model    string
	Kind     trace.Kind
}

// Bucket returns the bucket start.
func (k Key) Bucket() int64 { return k.BucketNs }

// KeyOf returns the key of span s in the bucket of resolution r containing its start.
func KeyOf(s *trace.Span, r Resolution) Key {
	return Key{
		BucketNs: r.Start(s.StartNs),
		Service:  s.Service,
		Provider: s.Provider,
		Model:    s.Model,
		Kind:     s.Kind,
	}
}

// UserKey identifies a row of rollup_user_hour or rollup_user_day. The empty
// UserID collects spans attributed to no user, shown as "(none)".
type UserKey struct {
	BucketNs int64
	UserID   string
}

// Bucket returns the bucket start.
func (k UserKey) Bucket() int64 { return k.BucketNs }

// UserKeyOf returns the user key of a span starting at startNs and attributed
// to userID (the writer resolves attribution).
func UserKeyOf(userID string, startNs int64, r Resolution) UserKey {
	return UserKey{BucketNs: r.Start(startNs), UserID: userID}
}
