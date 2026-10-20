// SPDX-License-Identifier: Apache-2.0

package ingest_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/ingest"
)

func TestNewInflightLimiter_RejectsNonPositive(t *testing.T) {
	t.Parallel()
	for _, limit := range []int64{0, -1} {
		_, err := ingest.NewInflightLimiter(limit)
		require.ErrorIs(t, err, ingest.ErrInvalidConfig)
	}
}

func TestInflightLimiter_TryAcquire(t *testing.T) {
	t.Parallel()
	l, err := ingest.NewInflightLimiter(100)
	require.NoError(t, err)
	assert.True(t, l.TryAcquire(60))
	assert.False(t, l.TryAcquire(41), "would exceed the limit")
	assert.True(t, l.TryAcquire(40), "exactly at the limit")
	assert.False(t, l.TryAcquire(1))
	assert.False(t, l.TryAcquire(-5), "negative amounts are refused")
	l.Release(60)
	assert.Equal(t, int64(40), l.InUse())
	assert.True(t, l.TryAcquire(60))
}

func TestInflightLimiter_ConcurrentNeverExceedsLimit(t *testing.T) {
	t.Parallel()
	const limit = 1000
	l, err := ingest.NewInflightLimiter(limit)
	require.NoError(t, err)
	var over atomic.Bool
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			n := int64(1 + g*37%300)
			for range 2000 {
				if l.TryAcquire(n) {
					if l.InUse() > limit {
						over.Store(true)
					}
					l.Release(n)
				}
			}
		})
	}
	wg.Wait()
	assert.False(t, over.Load())
	assert.Zero(t, l.InUse())
}
