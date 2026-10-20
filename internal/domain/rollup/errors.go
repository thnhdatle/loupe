// SPDX-License-Identifier: Apache-2.0

package rollup

import "errors"

var (
	// ErrEmptyHistogram reports a quantile asked of a histogram with no values.
	ErrEmptyHistogram = errors.New("rollup: empty histogram")
	// ErrInvalidQuantile reports a quantile outside [0, 1] or NaN.
	ErrInvalidQuantile = errors.New("rollup: quantile outside [0, 1]")
	// ErrCorruptHistogram reports a histogram blob that cannot be decoded.
	ErrCorruptHistogram = errors.New("rollup: corrupt histogram blob")
	// ErrUnsupportedVersion reports a histogram blob with an unknown version byte.
	ErrUnsupportedVersion = errors.New("rollup: unsupported histogram encoding version")
	// ErrInvalidRange reports a range that is empty or not aligned to whole hours.
	ErrInvalidRange = errors.New("rollup: invalid range")
)
