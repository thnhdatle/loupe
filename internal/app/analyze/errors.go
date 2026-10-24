// SPDX-License-Identifier: Apache-2.0

package analyze

import "errors"

var (
	ErrInvalidConfig = errors.New("analyze: invalid configuration")
	ErrBusy          = errors.New("analyze: an analysis of this trace is already running")
)
