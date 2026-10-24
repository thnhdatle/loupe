// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"fmt"
	"math"
)

const NanosPerUSD = 1_000_000_000

const MaxPriceDecimals = 9

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
	digits := intPart + fracPart
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
