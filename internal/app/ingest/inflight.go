// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"fmt"
	"sync/atomic"
)

const DefaultMaxInflightBytes = 64 << 20

type InflightLimiter struct {
	limit int64
	used  atomic.Int64
}

func NewInflightLimiter(limit int64) (*InflightLimiter, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: max inflight bytes must be positive", ErrInvalidConfig)
	}
	return &InflightLimiter{limit: limit}, nil
}

func (l *InflightLimiter) TryAcquire(n int64) bool {
	for {
		cur := l.used.Load()
		if n < 0 || cur+n > l.limit {
			return false
		}
		if l.used.CompareAndSwap(cur, cur+n) {
			return true
		}
	}
}

func (l *InflightLimiter) Release(n int64) { l.used.Add(-n) }

func (l *InflightLimiter) InUse() int64 { return l.used.Load() }
