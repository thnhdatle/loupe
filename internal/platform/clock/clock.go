// SPDX-License-Identifier: Apache-2.0

package clock

import "time"

// Clock abstracts time so components and tests can control it.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) Ticker
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// System is the Clock backed by the real wall clock.
type System struct{}

func (System) Now() time.Time { return time.Now() }

func (System) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (System) NewTicker(d time.Duration) Ticker { return systemTicker{t: time.NewTicker(d)} }

type systemTicker struct{ t *time.Ticker }

func (s systemTicker) C() <-chan time.Time { return s.t.C }

func (s systemTicker) Stop() { s.t.Stop() }
