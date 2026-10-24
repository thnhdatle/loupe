// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/hex"
	"fmt"
)

type ID [16]byte

type SpanID [8]byte

func (t ID) String() string { return hex.EncodeToString(t[:]) }

func (t ID) IsZero() bool { return t == ID{} }

func (s SpanID) String() string { return hex.EncodeToString(s[:]) }

func (s SpanID) IsZero() bool { return s == SpanID{} }

func ParseID(s string) (ID, error) {
	var id ID
	if err := decodeHexID(id[:], s); err != nil {
		return ID{}, fmt.Errorf("trace: parse trace id: %w", err)
	}
	return id, nil
}

func ParseSpanID(s string) (SpanID, error) {
	var id SpanID
	if err := decodeHexID(id[:], s); err != nil {
		return SpanID{}, fmt.Errorf("trace: parse span id: %w", err)
	}
	return id, nil
}

func IDFromBytes(b []byte) (ID, error) {
	var id ID
	if len(b) != len(id) {
		return ID{}, fmt.Errorf("%w: trace id has %d bytes, want %d", ErrInvalidID, len(b), len(id))
	}
	copy(id[:], b)
	return id, nil
}

func SpanIDFromBytes(b []byte) (SpanID, error) {
	var id SpanID
	if len(b) == 0 {
		return id, nil
	}
	if len(b) != len(id) {
		return SpanID{}, fmt.Errorf("%w: span id has %d bytes, want %d", ErrInvalidID, len(b), len(id))
	}
	copy(id[:], b)
	return id, nil
}

func decodeHexID(dst []byte, s string) error {
	if len(s) != 2*len(dst) {
		return fmt.Errorf("%w: %d hex characters, want %d", ErrInvalidID, len(s), 2*len(dst))
	}
	if _, err := hex.Decode(dst, []byte(s)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidID, err)
	}
	return nil
}
