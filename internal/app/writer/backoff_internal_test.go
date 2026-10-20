// SPDX-License-Identifier: Apache-2.0

package writer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/clock"
)

type nopStore struct{}

func (nopStore) WriteBatch(_ context.Context, _ *trace.Batch) (trace.BatchResult, error) {
	return trace.BatchResult{}, nil
}

func TestWriter_backoff_JitteredAndBounded(t *testing.T) {
	t.Parallel()
	w, err := New(nopStore{}, clock.NewFake(time.Unix(0, 0)), WithRetry(40, 100*time.Millisecond, 2*time.Second))
	require.NoError(t, err)
	for n := 1; n <= 40; n++ {
		nominal := min(100*time.Millisecond<<min(n-1, 10), 2*time.Second)
		for range 50 {
			d := w.backoff(n)
			assert.GreaterOrEqual(t, d, nominal/2, "retry %d", n)
			assert.LessOrEqual(t, d, nominal, "retry %d", n)
		}
	}
}

func TestFloorHour(t *testing.T) {
	t.Parallel()
	h := int64(time.Hour)
	tests := []struct {
		name   string
		in     int64
		expect int64
	}{
		{name: "zero", in: 0, expect: 0},
		{name: "inside hour", in: 3*h + 5, expect: 3 * h},
		{name: "on boundary", in: 2 * h, expect: 2 * h},
		{name: "negative", in: -1, expect: -h},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expect, floorHour(tc.in))
		})
	}
}
