// SPDX-License-Identifier: Apache-2.0

package clock

import (
	"sync"
	"time"
)

// Fake is a manually advanced Clock for deterministic tests. It is safe for
// concurrent use.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
}

type waiter struct {
	at     time.Time
	period time.Duration // zero for one-shot timers
	ch     chan time.Time
	done   bool
}

// NewFake returns a Fake whose current time is start.
func NewFake(start time.Time) *Fake { return &Fake{now: start} }

// Now returns the fake current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that receives once the fake time reaches Now()+d.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &waiter{at: f.now.Add(d), ch: make(chan time.Time, 1)}
	f.waiters = append(f.waiters, w)
	return w.ch
}

// NewTicker returns a ticker driven by Advance. It panics if d is not
// positive, matching time.NewTicker.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &waiter{at: f.now.Add(d), period: d, ch: make(chan time.Time, 1)}
	f.waiters = append(f.waiters, w)
	return &fakeTicker{f: f, w: w}
}

// Advance moves the fake time forward by d and fires every timer and ticker
// that became due, in time order. Ticks that find a full channel are dropped,
// as with time.Ticker.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	target := f.now.Add(d)
	for {
		next := f.nextDue(target)
		if next == nil {
			break
		}
		f.now = next.at
		select {
		case next.ch <- next.at:
		default:
		}
		if next.period > 0 {
			next.at = next.at.Add(next.period)
		} else {
			next.done = true
		}
	}
	f.now = target
	f.compact()
}

func (f *Fake) nextDue(limit time.Time) *waiter {
	var best *waiter
	for _, w := range f.waiters {
		if w.done || w.at.After(limit) {
			continue
		}
		if best == nil || w.at.Before(best.at) {
			best = w
		}
	}
	return best
}

func (f *Fake) compact() {
	live := f.waiters[:0]
	for _, w := range f.waiters {
		if !w.done {
			live = append(live, w)
		}
	}
	f.waiters = live
}

type fakeTicker struct {
	f *Fake
	w *waiter
}

func (t *fakeTicker) C() <-chan time.Time { return t.w.ch }

func (t *fakeTicker) Stop() {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	t.w.done = true
	t.f.compact()
}
