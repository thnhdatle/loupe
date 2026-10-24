// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"context"
	"fmt"
	"time"
)

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

func (p *Planner) measure(ctx context.Context) (int64, error) {
	size, err := p.store.Size(ctx)
	if err != nil {
		return 0, fmt.Errorf("retention: database size: %w", err)
	}
	p.stats.dbSize.Store(size)
	return size, nil
}
