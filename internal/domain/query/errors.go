// SPDX-License-Identifier: Apache-2.0

package query

import "errors"

var (
	// ErrInvalidCursor reports a cursor that is malformed or was issued for another sort or order.
	ErrInvalidCursor = errors.New("query: invalid cursor")
	// ErrInvalidLimit reports a page limit below zero or above MaxLimit.
	ErrInvalidLimit = errors.New("query: invalid limit")
	// ErrInvalidRange reports a time range whose from is not before its to.
	ErrInvalidRange = errors.New("query: invalid time range")
	// ErrInvalidUSD reports a malformed or out-of-range USD amount.
	ErrInvalidUSD = errors.New("query: invalid usd amount")
	// ErrInvalidSort reports an unknown sort key or order.
	ErrInvalidSort = errors.New("query: invalid sort")
	// ErrInvalidFilter reports a filter or request with invalid field values.
	ErrInvalidFilter = errors.New("query: invalid filter")
)
