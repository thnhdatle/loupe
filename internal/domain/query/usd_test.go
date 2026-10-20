// SPDX-License-Identifier: Apache-2.0

package query_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/query"
)

func TestParseUSD(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{"zero", "0", 0, false},
		{"whole", "3", 3_000_000_000, false},
		{"cents", "0.25", 250_000_000, false},
		{"nine digits", "0.000000001", 1, false},
		{"leading zeros", "007.5", 7_500_000_000, false},
		{"max int64 nanos", "9223372036.854775807", 9223372036854775807, false},
		{"overflow", "9223372036.854775808", 0, true},
		{"huge", "99999999999999999999", 0, true},
		{"ten digits", "0.0000000001", 0, true},
		{"empty", "", 0, true},
		{"negative", "-1", 0, true},
		{"plus", "+1", 0, true},
		{"no whole part", ".5", 0, true},
		{"trailing dot", "1.", 0, true},
		{"two dots", "1.2.3", 0, true},
		{"exponent", "1e3", 0, true},
		{"letters", "abc", 0, true},
		{"space", " 1", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := query.ParseUSD(tc.in)
			if tc.wantErr {
				require.ErrorIs(t, err, query.ErrInvalidUSD)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func FuzzParseUSD(f *testing.F) {
	for _, s := range []string{"0", "0.25", "1.000000001", "-1", "", ".", "9223372036.854775807", "1e3", "٣"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := query.ParseUSD(s)
		if err == nil && n < 0 {
			t.Fatalf("ParseUSD(%q) = %d, negative", s, n)
		}
	})
}
