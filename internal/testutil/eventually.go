// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"testing"
	"time"
)

const DefaultTimeout = 10 * time.Second

const pollInterval = time.Millisecond

// Eventually polls cond until it returns true and fails the test on timeout.
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
