// SPDX-License-Identifier: Apache-2.0

// Package clock provides the injectable Clock used by app and adapter code, a
// real implementation backed by the time package and a deterministic Fake for
// tests. Domain code never holds a Clock; it receives now as a parameter.
//
// Usage:
//
//	c := clock.NewFake(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
//	t := c.NewTicker(time.Second)
//	c.Advance(time.Second) // t.C() delivers one tick
package clock
