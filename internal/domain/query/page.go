// SPDX-License-Identifier: Apache-2.0

package query

import "fmt"

// Page limits.
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Page selects one page of a list: Limit items after the opaque Cursor
// (empty for the first page).
type Page struct {
	Limit  int
	Cursor string
}

// NormalizeLimit maps 0 to DefaultLimit and rejects values below 0 or above MaxLimit.
func NormalizeLimit(n int) (int, error) {
	switch {
	case n == 0:
		return DefaultLimit, nil
	case n < 0 || n > MaxLimit:
		return 0, fmt.Errorf("%w: %d not in 1..%d", ErrInvalidLimit, n, MaxLimit)
	default:
		return n, nil
	}
}
