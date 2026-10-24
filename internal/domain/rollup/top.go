// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"cmp"
	"slices"
)

const DefaultTopUsers = 50

type UserTotal struct {
	UserID    string
	Aggregate Aggregate
}

type TopUsersResult struct {
	Top        []UserTotal
	Other      Aggregate
	OtherUsers int
}

func TopUsers(totals map[string]*Aggregate, n int) TopUsersResult {
	ids := make([]string, 0, len(totals))
	for id := range totals {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		x, y := totals[a].Measures, totals[b].Measures
		if c := cmp.Compare(y.CostNanos, x.CostNanos); c != 0 {
			return c
		}
		if c := cmp.Compare(y.Count, x.Count); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	n = min(max(n, 0), len(ids))
	res := TopUsersResult{Top: make([]UserTotal, 0, n), OtherUsers: len(ids) - n}
	for _, id := range ids[:n] {
		res.Top = append(res.Top, UserTotal{UserID: id, Aggregate: totals[id].Clone()})
	}
	for _, id := range ids[n:] {
		res.Other.Merge(totals[id])
	}
	return res
}
