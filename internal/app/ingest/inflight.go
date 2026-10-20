// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"fmt"
	"sync/atomic"
)

// DefaultMaxInflightBytes is receiver.max_inflight_bytes.
const DefaultMaxInflightBytes = 64 << 20

// InflightLimiter accounts decompressed request bytes that receivers hold
// before their spans are enqueued (receiver.max_inflight_bytes). The
// HTTP receiver acquires bytes as it reads them; the gRPC receiver reserves
// min(max_request_bytes, 1 MiB) when admitting a call and adjusts to the actual
// size on receipt. Bytes are released when the request's spans are enqueued
// or the request fails. It is safe for concurrent use.
type InflightLimiter struct {
	limit int64
	used  atomic.Int64
}

// NewInflightLimiter returns a limiter admitting up to limit bytes in flight.
func NewInflightLimiter(limit int64) (*InflightLimiter, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: max inflight bytes must be positive", ErrInvalidConfig)
	}
	return &InflightLimiter{limit: limit}, nil
}

// TryAcquire takes n more bytes if the total stays within the limit and
// reports whether it did. A false result means the request must fail with a
// retryable status (HTTP 429, gRPC RESOURCE_EXHAUSTED).
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

// Release returns n previously acquired bytes.
func (l *InflightLimiter) Release(n int64) { l.used.Add(-n) }

// InUse returns the bytes currently held.
func (l *InflightLimiter) InUse() int64 { return l.used.Load() }
