// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"testing"
	"time"
)

// DefaultTimeout is a generous bound for Eventually in unit tests: conditions
// normally hold within milliseconds, but -race on a loaded CI machine is slow.
const DefaultTimeout = 10 * time.Second

// pollInterval is how often Eventually re-evaluates its condition.
const pollInterval = time.Millisecond

// Eventually polls cond until it returns true and fails the test if that does
// not happen within timeout. cond may have side effects, such as advancing a
// fake clock, which lets a test drive timers in another goroutine without
// knowing exactly when that goroutine starts waiting. Call it only from the
// test goroutine.
func Eventually(tb testing.TB, cond func() bool, timeout time.Duration) {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for !cond() {
		if time.Now().After(deadline) {
			tb.Fatalf("testutil: condition not met within %s", timeout)
		}
		<-tick.C
	}
}
