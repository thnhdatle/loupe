// SPDX-License-Identifier: Apache-2.0

package cost_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/cost"
)

func TestParsePrice_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want int64
	}{
		{name: "integer", text: "3", want: 3_000_000_000},
		{name: "zero", text: "0", want: 0},
		{name: "zero with decimals", text: "0.000000000", want: 0},
		{name: "two decimals", text: "2.50", want: 2_500_000_000},
		{name: "three decimals", text: "0.075", want: 75_000_000},
		{name: "nine decimals", text: "15.000000001", want: 15_000_000_001},
		{name: "one nano", text: "0.000000001", want: 1},
		{name: "leading zeros", text: "007.5", want: 7_500_000_000},
		{name: "not representable as float64", text: "0.300000001", want: 300_000_001},
		{name: "max int64", text: "9223372036.854775807", want: math.MaxInt64},
		{name: "max whole USD", text: "9223372036", want: 9_223_372_036_000_000_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := cost.ParsePrice(tc.text)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParsePrice_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want error
	}{
		{name: "empty", text: "", want: cost.ErrInvalidPrice},
		{name: "ten decimals", text: "0.0000000001", want: cost.ErrTooManyDecimals},
		{name: "trailing zero tenth decimal", text: "1.5000000000", want: cost.ErrTooManyDecimals},
		{name: "negative", text: "-1", want: cost.ErrNegativePrice},
		{name: "negative zero", text: "-0", want: cost.ErrNegativePrice},
		{name: "plus sign", text: "+1", want: cost.ErrInvalidPrice},
		{name: "exponent", text: "1e-6", want: cost.ErrInvalidPrice},
		{name: "underscore", text: "1_000", want: cost.ErrInvalidPrice},
		{name: "leading dot", text: ".5", want: cost.ErrInvalidPrice},
		{name: "trailing dot", text: "5.", want: cost.ErrInvalidPrice},
		{name: "two dots", text: "1.2.3", want: cost.ErrInvalidPrice},
		{name: "whitespace", text: " 1", want: cost.ErrInvalidPrice},
		{name: "letters", text: "abc", want: cost.ErrInvalidPrice},
		{name: "infinity", text: ".inf", want: cost.ErrInvalidPrice},
		{name: "overflow by one nano", text: "9223372036.854775808", want: cost.ErrPriceOverflow},
		{name: "overflow whole", text: "9223372037", want: cost.ErrPriceOverflow},
		{name: "overflow many digits", text: "99999999999999999999", want: cost.ErrPriceOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := cost.ParsePrice(tc.text)
			require.ErrorIs(t, err, tc.want)
			assert.Contains(t, err.Error(), "cost: parse price")
		})
	}
}

func TestFormatPrice_RoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		nanos int64
		want  string
	}{
		{name: "zero", nanos: 0, want: "0"},
		{name: "whole", nanos: 3_000_000_000, want: "3"},
		{name: "trailing zeros trimmed", nanos: 2_500_000_000, want: "2.5"},
		{name: "one nano", nanos: 1, want: "0.000000001"},
		{name: "max", nanos: math.MaxInt64, want: "9223372036.854775807"},
		{name: "negative", nanos: -75_000_000, want: "-0.075"},
		{name: "min", nanos: math.MinInt64, want: "-9223372036.854775808"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cost.FormatPrice(tc.nanos)
			assert.Equal(t, tc.want, got)
			if tc.nanos >= 0 {
				back, err := cost.ParsePrice(got)
				require.NoError(t, err)
				assert.Equal(t, tc.nanos, back)
			}
		})
	}
}

func FuzzParsePrice(f *testing.F) {
	for _, s := range []string{"0", "2.50", "0.000000001", "9223372036.854775807", "1e3", "-1", ".5", "1.0000000001"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := cost.ParsePrice(s)
		if err != nil {
			return
		}
		if n < 0 {
			t.Fatalf("ParsePrice(%q) = %d, negative", s, n)
		}
		back, err := cost.ParsePrice(cost.FormatPrice(n))
		if err != nil || back != n {
			t.Fatalf("round trip of %q: %d -> %q -> %d, %v", s, n, cost.FormatPrice(n), back, err)
		}
	})
}
