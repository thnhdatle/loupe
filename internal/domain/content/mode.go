// SPDX-License-Identifier: Apache-2.0

package content

import "fmt"

// Mode is the content.capture setting.
type Mode string

// Capture modes.
const (
	// ModeOff never stores content; a marker records that content arrived.
	ModeOff Mode = "off"
	// ModeOn stores content text, redacted and truncated.
	ModeOn Mode = "on"
	// ModeMetadataOnly stores roles and lengths but no text.
	ModeMetadataOnly Mode = "metadata_only"
)

// ParseMode converts a configuration string into a Mode.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case ModeOff, ModeOn, ModeMetadataOnly:
		return m, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidMode, s)
	}
}
