// SPDX-License-Identifier: Apache-2.0

package content

import "errors"

var (
	ErrInvalidMode = errors.New("content: invalid capture mode")
	ErrInvalidRule = errors.New("content: invalid redaction rule")
)
