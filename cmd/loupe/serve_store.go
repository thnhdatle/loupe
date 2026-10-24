// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/metrics"
)

type observedStore struct {
	trace.SpanStore
	m *metrics.Metrics
}

func (o observedStore) WriteBatch(ctx context.Context, b *trace.Batch) (trace.BatchResult, error) {
	start := time.Now()
	res, err := o.SpanStore.WriteBatch(ctx, b)
	if err != nil {
		return res, fmt.Errorf("write batch: %w", err)
	}
	o.m.ObserveBatch(time.Since(start), len(b.Spans))
	for kind, n := range res.UsageDuplicates {
		o.m.AddUsageDuplicate(string(kind), int64(n))
	}
	return res, nil
}
