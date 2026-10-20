// SPDX-License-Identifier: Apache-2.0

package query

import "fmt"

// Sort is the key a trace list is ordered by.
type Sort string

// Sort keys.
const (
	SortStart    Sort = "start"
	SortCost     Sort = "cost"
	SortDuration Sort = "duration"
)

// Order is the sort direction.
type Order string

// Sort directions; descending is the default.
const (
	OrderDesc Order = "desc"
	OrderAsc  Order = "asc"
)

// ParseSort parses a sort key; empty means SortStart.
func ParseSort(s string) (Sort, error) {
	switch Sort(s) {
	case "":
		return SortStart, nil
	case SortStart, SortCost, SortDuration:
		return Sort(s), nil
	default:
		return "", fmt.Errorf("%w: unknown sort %q", ErrInvalidSort, s)
	}
}

// ParseOrder parses a sort direction; empty means OrderDesc.
func ParseOrder(s string) (Order, error) {
	switch Order(s) {
	case "":
		return OrderDesc, nil
	case OrderDesc, OrderAsc:
		return Order(s), nil
	default:
		return "", fmt.Errorf("%w: unknown order %q", ErrInvalidSort, s)
	}
}
