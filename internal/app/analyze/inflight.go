// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"sync"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type inflight struct {
	mu  sync.Mutex
	ids map[trace.ID]struct{}
}

func (f *inflight) acquire(id trace.ID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, busy := f.ids[id]; busy {
		return false
	}
	if f.ids == nil {
		f.ids = map[trace.ID]struct{}{}
	}
	f.ids[id] = struct{}{}
	return true
}

func (f *inflight) release(id trace.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.ids, id)
}
