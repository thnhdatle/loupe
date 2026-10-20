// SPDX-License-Identifier: Apache-2.0

package clock

import "time"

// Clock abstracts time so that components and tests can control it.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After returns a channel that receives the time once d has elapsed.
	After(d time.Duration) <-chan time.Time
	// NewTicker returns a ticker that fires every d. d must be positive.
	NewTicker(d time.Duration) Ticker
}

// Ticker is the subset of time.Ticker used by Loupe.
type Ticker interface {
	// C returns the channel on which ticks are delivered.
	C() <-chan time.Time
	// Stop turns the ticker off. No ticks are delivered after Stop returns.
	Stop()
}

// System is the Clock backed by the real wall clock.
type System struct{}

// Now returns time.Now().
func (System) Now() time.Time { return time.Now() }

// After wraps time.After.
func (System) After(d time.Duration) <-chan time.Time { return time.After(d) }

// NewTicker wraps time.NewTicker.
func (System) NewTicker(d time.Duration) Ticker { return systemTicker{t: time.NewTicker(d)} }

type systemTicker struct{ t *time.Ticker }

func (s systemTicker) C() <-chan time.Time { return s.t.C }

func (s systemTicker) Stop() { s.t.Stop() }
