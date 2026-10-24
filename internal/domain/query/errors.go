// SPDX-License-Identifier: Apache-2.0

package query

import "errors"

var (
	ErrInvalidCursor = errors.New("query: invalid cursor")
	ErrInvalidLimit  = errors.New("query: invalid limit")
	ErrInvalidRange  = errors.New("query: invalid time range")
	ErrInvalidUSD    = errors.New("query: invalid usd amount")
	ErrInvalidSort   = errors.New("query: invalid sort")
	ErrInvalidFilter = errors.New("query: invalid filter")
)
