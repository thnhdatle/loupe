// SPDX-License-Identifier: Apache-2.0

package trace_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func genUsage(t *rapid.T, label string) trace.Usage {
	n := rapid.Int64Range(0, 1<<40)
	return trace.Usage{
		Input: n.Draw(t, label+".in"), Output: n.Draw(t, label+".out"),
		CacheRead: n.Draw(t, label+".cr"), CacheWrite: n.Draw(t, label+".cw"),
		Reasoning: n.Draw(t, label+".r"),
	}
}

func TestUsage_AddSubInverseProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a, b := genUsage(t, "a"), genUsage(t, "b")
		if got := a.Add(b).Sub(b); got != a {
			t.Fatalf("(a+b)-b = %+v, want %+v", got, a)
		}
		if a.Add(b) != b.Add(a) {
			t.Fatal("Add is not commutative")
		}
	})
}
