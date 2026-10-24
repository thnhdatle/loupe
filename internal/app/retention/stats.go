// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"slices"
	"sync/atomic"
)

type Stats struct {
	Passes         int64
	PassErrors     int64
	Deleted        map[Class]int64
	CapDeleted     map[Class]int64
	CapExceeded    int64
	CapUnmet       int64
	CheckpointBusy int64
	DBSizeBytes    int64
}

type counters struct {
	passes, passErrors, capExceeded, capUnmet, checkpointBusy, dbSize atomic.Int64

	deleted    [6]atomic.Int64
	capContent atomic.Int64
	capSpans   atomic.Int64
}

func classIndex(c Class) int { return slices.Index(Classes(), c) }

func (c *counters) snapshot() Stats {
	s := Stats{
		Passes:         c.passes.Load(),
		PassErrors:     c.passErrors.Load(),
		Deleted:        make(map[Class]int64, len(c.deleted)),
		CapDeleted:     map[Class]int64{ClassContent: c.capContent.Load(), ClassSpans: c.capSpans.Load()},
		CapExceeded:    c.capExceeded.Load(),
		CapUnmet:       c.capUnmet.Load(),
		CheckpointBusy: c.checkpointBusy.Load(),
		DBSizeBytes:    c.dbSize.Load(),
	}
	for _, cl := range Classes() {
		s.Deleted[cl] = c.deleted[classIndex(cl)].Load()
	}
	return s
}
