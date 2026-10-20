// SPDX-License-Identifier: Apache-2.0

// Package testutil holds helpers shared by tests only; non-test code never
// imports it. Fakes of domain ports live in subpackages named fake<port>.
//
// Usage:
//
//	testutil.Eventually(t, func() bool { return store.Calls() == 1 }, 5*time.Second)
package testutil
