// SPDX-License-Identifier: Apache-2.0

package cost_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/cost"
)

func usd(t *testing.T, text string) int64 {
	t.Helper()
	n, err := cost.ParsePrice(text)
	require.NoError(t, err)
	return n
}

func opt(t *testing.T, text string) *int64 {
	t.Helper()
	n := usd(t, text)
	return &n
}

func day(s string) time.Time {
	ts, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return ts
}

func entry(t *testing.T, key, in, out, from string) cost.Price {
	t.Helper()
	f := strings.Fields(key)
	require.Len(t, f, 3, key)
	provider := f[1]
	if provider == "-" {
		provider = ""
	}
	return cost.Price{
		Match: cost.Match(f[0]), Provider: provider, Model: f[2],
		InputPerMtok: usd(t, in), OutputPerMtok: usd(t, out),
		EffectiveFrom: day(from), AsOf: day("2026-10-01"), Source: "test",
	}
}
