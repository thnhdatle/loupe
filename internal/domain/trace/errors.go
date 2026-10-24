// SPDX-License-Identifier: Apache-2.0

package trace

import "errors"

var (
	ErrInvalidID    = errors.New("trace: invalid id")
	ErrOverloaded   = errors.New("trace: ingest overloaded")
	ErrShuttingDown = errors.New("trace: shutting down")
	ErrTooManySpans = errors.New("trace: too many spans in request")
	ErrNotFound     = errors.New("trace: not found")
)
