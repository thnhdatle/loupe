// SPDX-License-Identifier: Apache-2.0

package content

import "errors"

var (
	// ErrInvalidMode reports an unknown content.capture mode.
	ErrInvalidMode = errors.New("content: invalid capture mode")
	// ErrInvalidRule reports an unknown preset or an invalid custom redaction rule.
	ErrInvalidRule = errors.New("content: invalid redaction rule")
)
