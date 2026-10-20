// SPDX-License-Identifier: Apache-2.0

// Package fakespanstore is an in-memory trace.SpanStore for unit tests, with
// injectable errors and a gate that holds writes to simulate latency.
//
// Usage:
//
//	store := fakespanstore.New()
//	store.FailNext(errors.New("disk I/O error"))
//	release := store.Hold()
//	defer release()
package fakespanstore
