// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Unit parsing errors, wrapped by ParseDuration and ParseSize.
var (
	// ErrInvalidDuration reports text that is not a Go duration with an optional day count.
	ErrInvalidDuration = errors.New("invalid duration")
	// ErrInvalidSize reports text that is not a byte count with an optional binary suffix.
	ErrInvalidSize = errors.New("invalid size")
)

const day = 24 * time.Hour

// Duration is a time.Duration written as a Go duration string with an optional
// leading day count ("30s", "14d", "1d12h"). A bare "0" is accepted.
type Duration time.Duration

// ParseDuration parses s. Negative values are rejected.
func ParseDuration(s string) (time.Duration, error) {
	orig := s
	if s == "0" {
		return 0, nil
	}
	var total time.Duration
	if i := strings.IndexByte(s, 'd'); i > 0 && isDigits(s[:i]) {
		n, err := strconv.ParseInt(s[:i], 10, 64)
		if err != nil || n > int64(math.MaxInt64/day) {
			return 0, fmt.Errorf("config: %w %q", ErrInvalidDuration, orig)
		}
		total, s = time.Duration(n)*day, s[i+1:]
		if s == "" {
			return total, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 || total > math.MaxInt64-d {
		return 0, fmt.Errorf("config: %w %q", ErrInvalidDuration, orig)
	}
	return total + d, nil
}

// FormatDuration renders d with a day count when it is a whole number of days.
func FormatDuration(d time.Duration) string {
	if d > 0 && d%day == 0 {
		return strconv.FormatInt(int64(d/day), 10) + "d"
	}
	return d.String()
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return FormatDuration(time.Duration(d)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Size is a byte count written with an optional binary suffix ("16MiB", "64KiB",
// "1048576"). Suffixes are B, KiB, MiB, GiB and TiB, case-insensitive.
type Size int64

var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"TiB", 1 << 40},
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
}

// ParseSize parses s into bytes. Negative values and overflow are rejected.
func ParseSize(s string) (int64, error) {
	num, mult := s, int64(1)
	lower := strings.ToLower(s)
	for _, u := range sizeUnits {
		if strings.HasSuffix(lower, strings.ToLower(u.suffix)) {
			num, mult = s[:len(s)-len(u.suffix)], u.mult
			break
		}
	}
	if mult == 1 && strings.HasSuffix(lower, "b") {
		num = s[:len(s)-1]
	}
	if !isDigits(num) {
		return 0, fmt.Errorf("config: %w %q", ErrInvalidSize, s)
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n > math.MaxInt64/mult {
		return 0, fmt.Errorf("config: %w %q", ErrInvalidSize, s)
	}
	return n * mult, nil
}

// FormatSize renders n with the largest binary suffix that divides it exactly.
func FormatSize(n int64) string {
	if n > 0 {
		for _, u := range sizeUnits {
			if n%u.mult == 0 {
				return strconv.FormatInt(n/u.mult, 10) + u.suffix
			}
		}
	}
	return strconv.FormatInt(n, 10)
}

// Bytes returns the value as a byte count.
func (s Size) Bytes() int64 { return int64(s) }

// String implements fmt.Stringer.
func (s Size) String() string { return FormatSize(int64(s)) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *Size) UnmarshalText(text []byte) error {
	v, err := ParseSize(string(text))
	if err != nil {
		return err
	}
	*s = Size(v)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (s Size) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
