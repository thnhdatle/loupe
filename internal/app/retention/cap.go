// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"context"
	"fmt"
	"time"
)

// enforceCap deletes the oldest content, then the oldest spans, one
// retention.batch job at a time, compacting and re-measuring after each, until
// the database is within retention.max_db_bytes. Rows younger than CapFloor are
// never deleted: when only they remain the pass logs an error and ingest goes
// on. A busy checkpoint ends the cap phase for this pass, because the size
// cannot drop until the WAL is checkpointed and deleting on would overshoot.
func (p *Planner) enforceCap(ctx context.Context, now time.Time) error {
	size, err := p.measure(ctx)
	if err != nil || size <= p.cfg.MaxDBBytes {
		return err
	}
	p.stats.capExceeded.Add(1)
	floor := now.Add(-CapFloor)
	var content, spans int64
	defer func() {
		p.log.Warn("database above retention.max_db_bytes; deleted oldest data",
			"bytes", size, "count", content+spans)
	}()
	contentLeft := true
	for size > p.cfg.MaxDBBytes {
		var n int64
		if contentLeft {
			if n, err = p.purgeOnce(ctx, "retention_disk_cap_content", ClassContent, floor); err != nil {
				return err
			}
			content += n
			p.stats.capContent.Add(n)
			contentLeft = n > 0
		}
		if !contentLeft {
			if n, err = p.purgeOnce(ctx, "retention_disk_cap_spans", ClassSpans, floor); err != nil {
				return err
			}
			spans += n
			p.stats.capSpans.Add(n)
		}
		if n == 0 {
			p.stats.capUnmet.Add(1)
			p.log.Error("database above retention.max_db_bytes with only the last hour of data left; ingest continues",
				"bytes", size)
			return nil
		}
		busy, err := p.compact(ctx)
		if err != nil || busy {
			return err
		}
		if size, err = p.measure(ctx); err != nil {
			return err
		}
	}
	return nil
}

// measure reads the database size and records it.
func (p *Planner) measure(ctx context.Context) (int64, error) {
	size, err := p.store.Size(ctx)
	if err != nil {
		return 0, fmt.Errorf("retention: database size: %w", err)
	}
	p.stats.dbSize.Store(size)
	return size, nil
}
