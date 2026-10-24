// SPDX-License-Identifier: Apache-2.0

package rollup

import "errors"

var (
	ErrEmptyHistogram     = errors.New("rollup: empty histogram")
	ErrInvalidQuantile    = errors.New("rollup: quantile outside [0, 1]")
	ErrCorruptHistogram   = errors.New("rollup: corrupt histogram blob")
	ErrUnsupportedVersion = errors.New("rollup: unsupported histogram encoding version")
	ErrInvalidRange       = errors.New("rollup: invalid range")
)
