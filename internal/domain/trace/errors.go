// SPDX-License-Identifier: Apache-2.0

package trace

import "errors"

var (
	// ErrInvalidID reports a trace or span id of the wrong length or encoding.
	ErrInvalidID = errors.New("trace: invalid id")
	// ErrOverloaded reports that the ingest queue or in-flight limit is full;
	// the receiver answers with a retryable status (HTTP 429, gRPC RESOURCE_EXHAUSTED).
	ErrOverloaded = errors.New("trace: ingest overloaded")
	// ErrShuttingDown reports that ingest no longer accepts work (HTTP 503, gRPC UNAVAILABLE).
	ErrShuttingDown = errors.New("trace: shutting down")
	// ErrTooManySpans reports a request above receiver.max_spans_per_request.
	ErrTooManySpans = errors.New("trace: too many spans in request")
	// ErrNotFound reports that a requested trace, session or job does not exist.
	ErrNotFound = errors.New("trace: not found")
)
