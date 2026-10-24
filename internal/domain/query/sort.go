// SPDX-License-Identifier: Apache-2.0

package query

import "fmt"

type Sort string

const (
	SortStart    Sort = "start"
	SortCost     Sort = "cost"
	SortDuration Sort = "duration"
)

type Order string

const (
	OrderDesc Order = "desc"
	OrderAsc  Order = "asc"
)

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
