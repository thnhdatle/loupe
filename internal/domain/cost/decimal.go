// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"fmt"
	"math"
)

// NanosPerUSD is the number of nano-USD in one USD; prices and costs are integers in this unit.
const NanosPerUSD = 1_000_000_000

// MaxPriceDecimals is the number of fractional digits a price may have (nano-USD resolution).
const MaxPriceDecimals = 9

// ParsePrice converts the text of a YAML scalar holding USD per million tokens
// into integer nano-USD per million tokens, exactly and without float64.
//
// Accepted syntax is digits, optionally followed by a dot and at least one more
// digit ("3", "0.075", "15.000000001"). Signs, exponents, underscores,
// whitespace, a leading or trailing dot and more than MaxPriceDecimals
// fractional digits (trailing zeros included: "1.5000000000" is rejected) are
// errors.
func ParsePrice(text string) (int64, error) {
	if text != "" && text[0] == '-' {
		return 0, fmt.Errorf("cost: parse price %q: %w", text, ErrNegativePrice)
	}
	intPart, fracPart, err := splitDecimal(text)
	if err != nil {
		return 0, fmt.Errorf("cost: parse price %q: %w", text, err)
	}
	if len(fracPart) > MaxPriceDecimals {
		return 0, fmt.Errorf("cost: parse price %q: %w", text, ErrTooManyDecimals)
	}
	var n uint64
	digits := intPart + fracPart // validated ASCII digits
	for i := range len(digits) {
		d := uint64(digits[i] - '0')
		if n > (math.MaxInt64-d)/10 {
			return 0, fmt.Errorf("cost: parse price %q: %w", text, ErrPriceOverflow)
		}
		n = n*10 + d
	}
	for range MaxPriceDecimals - len(fracPart) {
		if n > math.MaxInt64/10 {
			return 0, fmt.Errorf("cost: parse price %q: %w", text, ErrPriceOverflow)
		}
		n *= 10
	}
	return int64(n), nil
}

// FormatPrice renders nano-USD per million tokens as the shortest decimal USD
// text that ParsePrice maps back to the same value.
func FormatPrice(nanos int64) string {
	sign := ""
	u := uint64(nanos) //nolint:gosec // G115: two's complement negation below handles MinInt64.
	if nanos < 0 {
		sign, u = "-", -u
	}
	whole, frac := u/NanosPerUSD, u%NanosPerUSD
	if frac == 0 {
		return fmt.Sprintf("%s%d", sign, whole)
	}
	digits := fmt.Sprintf("%09d", frac)
	for digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	return fmt.Sprintf("%s%d.%s", sign, whole, digits)
}

// splitDecimal splits "123.456" into "123" and "456", validating that both
// parts are ASCII digits and the integer part is non-empty.
func splitDecimal(text string) (intPart, fracPart string, err error) {
	dot := -1
	for i := range len(text) {
		c := text[i]
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && dot < 0:
			dot = i
		default:
			return "", "", ErrInvalidPrice
		}
	}
	if dot < 0 {
		intPart = text
	} else {
		intPart, fracPart = text[:dot], text[dot+1:]
		if fracPart == "" {
			return "", "", ErrInvalidPrice
		}
	}
	if intPart == "" {
		return "", "", ErrInvalidPrice
	}
	return intPart, fracPart, nil
}
