// SPDX-License-Identifier: Apache-2.0

package content

import "fmt"

type Mode string

const (
	ModeOff          Mode = "off"
	ModeOn           Mode = "on"
	ModeMetadataOnly Mode = "metadata_only"
)

func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case ModeOff, ModeOn, ModeMetadataOnly:
		return m, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidMode, s)
	}
}
