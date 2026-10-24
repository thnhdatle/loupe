// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type Resolution uint8

const (
	ResolutionHour Resolution = iota + 1
	ResolutionDay
)

const (
	HourNs = int64(time.Hour)
	DayNs  = 24 * HourNs
)

func (r Resolution) Duration() int64 {
	if r == ResolutionDay {
		return DayNs
	}
	return HourNs
}

func (r Resolution) Start(ns int64) int64 { return floorTo(ns, r.Duration()) }

func (r Resolution) String() string {
	if r == ResolutionDay {
		return "day"
	}
	return "hour"
}

func HourStart(ns int64) int64 { return floorTo(ns, HourNs) }

func DayStart(ns int64) int64 { return floorTo(ns, DayNs) }

func floorTo(ns, step int64) int64 {
	r := ns % step
	if r < 0 {
		r += step
	}
	return ns - r
}

type Key struct {
	BucketNs int64
	Service  string
	Provider string
	Model    string
	Kind     trace.Kind
}

func (k Key) Bucket() int64 { return k.BucketNs }

func KeyOf(s *trace.Span, r Resolution) Key {
	return Key{
		BucketNs: r.Start(s.StartNs),
		Service:  s.Service,
		Provider: s.Provider,
		Model:    s.Model,
		Kind:     s.Kind,
	}
}

type UserKey struct {
	BucketNs int64
	UserID   string
}

func (k UserKey) Bucket() int64 { return k.BucketNs }

func UserKeyOf(userID string, startNs int64, r Resolution) UserKey {
	return UserKey{BucketNs: r.Start(startNs), UserID: userID}
}
