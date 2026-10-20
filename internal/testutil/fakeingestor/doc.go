// SPDX-License-Identifier: Apache-2.0

// Package fakeingestor is an in-memory trace.Ingestor for receiver tests: it records
// what it is given and can return a canned result or error.
//
// Usage:
//
//	ing := fakeingestor.New()
//	ing.SetError(trace.ErrOverloaded)
//	spans := ing.Spans()
package fakeingestor
