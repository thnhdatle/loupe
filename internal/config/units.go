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

var (
	ErrInvalidDuration = errors.New("invalid duration")
	ErrInvalidSize     = errors.New("invalid size")
)

const day = 24 * time.Hour

type Duration time.Duration

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

func FormatDuration(d time.Duration) string {
	if d > 0 && d%day == 0 {
		return strconv.FormatInt(int64(d/day), 10) + "d"
	}
	return d.String()
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return FormatDuration(time.Duration(d)) }

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

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

func (s Size) Bytes() int64 { return int64(s) }

func (s Size) String() string { return FormatSize(int64(s)) }

func (s *Size) UnmarshalText(text []byte) error {
	v, err := ParseSize(string(text))
	if err != nil {
		return err
	}
	*s = Size(v)
	return nil
}

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
