// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
)

func userAgg(cost, count int64, latencies ...int64) *rollup.Aggregate {
	a := &rollup.Aggregate{Measures: rollup.Measures{Count: count, CostNanos: cost}}
	for _, v := range latencies {
		a.Histogram.Add(v)
	}
	return a
}

func topIDs(r rollup.TopUsersResult) []string {
	ids := make([]string, 0, len(r.Top))
	for _, u := range r.Top {
		ids = append(ids, u.UserID)
	}
	return ids
}

func TestTopUsers_RankingAndTieBreak(t *testing.T) {
	t.Parallel()
	totals := map[string]*rollup.Aggregate{
		"carol": userAgg(5, 1, 1e6),
		"bob":   userAgg(5, 3, 2e6),
		"alice": userAgg(5, 3, 3e6),
		"dave":  userAgg(9, 1, 4e6),
		"":      userAgg(0, 8, 5e6),
		"erin":  userAgg(0, 8, 6e6),
	}
	r := rollup.TopUsers(totals, 4)
	assert.Equal(t, []string{"dave", "alice", "bob", "carol"}, topIDs(r))
	assert.Equal(t, 2, r.OtherUsers)
	assert.Equal(t, rollup.Measures{Count: 16}, r.Other.Measures)
	assert.Equal(t, uint64(2), r.Other.Histogram.Count())

	all := rollup.TopUsers(totals, 10)
	assert.Equal(t, []string{"dave", "alice", "bob", "carol", "", "erin"}, topIDs(all))
	assert.Zero(t, all.OtherUsers)
	assert.True(t, all.Other.Histogram.IsEmpty())
}

func TestTopUsers_NonPositiveN(t *testing.T) {
	t.Parallel()
	totals := map[string]*rollup.Aggregate{"a": userAgg(1, 1), "b": userAgg(2, 1)}
	for _, n := range []int{0, -3} {
		r := rollup.TopUsers(totals, n)
		assert.Empty(t, r.Top)
		assert.Equal(t, 2, r.OtherUsers)
		assert.Equal(t, int64(3), r.Other.Measures.CostNanos)
	}
}

func TestTopUsers_DefaultFiftyPlusOther(t *testing.T) {
	t.Parallel()
	totals := make(map[string]*rollup.Aggregate, 80)
	for i := range 80 {
		totals[fmt.Sprintf("user-%02d", i)] = userAgg(int64(i), 1, 1e6)
	}
	r := rollup.TopUsers(totals, rollup.DefaultTopUsers)
	require.Len(t, r.Top, 50)
	assert.Equal(t, "user-79", r.Top[0].UserID)
	assert.Equal(t, "user-30", r.Top[49].UserID)
	assert.Equal(t, 30, r.OtherUsers)
	assert.Equal(t, int64(30), r.Other.Measures.Count)
}

func TestTopUsers_DoesNotShareStorage(t *testing.T) {
	t.Parallel()
	totals := map[string]*rollup.Aggregate{"a": userAgg(2, 1, 1e6), "b": userAgg(1, 1, 1e6)}
	r := rollup.TopUsers(totals, 1)
	totals["a"].Histogram.Add(5e9)
	totals["b"].Histogram.Add(5e9)
	assert.Equal(t, uint64(1), r.Top[0].Aggregate.Histogram.Count())
	assert.Equal(t, uint64(1), r.Other.Histogram.Count())
}
