// SPDX-License-Identifier: Apache-2.0

package query

import (
	"fmt"
	"math"
	"strings"
)

const usdFracDigits = 9

// ParseUSD converts a non-negative decimal USD string such as "0.25" into
// nano-USD without floating point. At most nine fractional digits are accepted.
func ParseUSD(s string) (int64, error) {
	whole, frac, _ := strings.Cut(s, ".")
	hasFrac := strings.Contains(s, ".")
	if whole == "" || (hasFrac && frac == "") {
		return 0, fmt.Errorf("%w: %q", ErrInvalidUSD, s)
	}
	if len(frac) > usdFracDigits {
		return 0, fmt.Errorf("%w: %q has more than %d fractional digits", ErrInvalidUSD, s, usdFracDigits)
	}
	frac += strings.Repeat("0", usdFracDigits-len(frac))
	var n uint64
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: %q", ErrInvalidUSD, s)
		}
		d := uint64(r - '0')
		if n > (math.MaxInt64-d)/10 {
			return 0, fmt.Errorf("%w: %q out of range", ErrInvalidUSD, s)
		}
		n = n*10 + d
	}
	return int64(n), nil
}
