// SPDX-License-Identifier: Apache-2.0

package query

import "fmt"

const (
	DefaultLimit = 50
	MaxLimit     = 500
)

type Page struct {
	Limit  int
	Cursor string
}

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
