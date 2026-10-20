// SPDX-License-Identifier: Apache-2.0

// Package httpmw holds the HTTP building blocks shared by the REST and admin
// handlers: request ids, the JSON error envelope, panic recovery
// and constant-time bearer-token checks.
//
// Usage:
//
//	h := httpmw.Chain(mux, httpmw.Recover(log), httpmw.RequestID(clock.System{}))
package httpmw
