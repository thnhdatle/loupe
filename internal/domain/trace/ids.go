// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/hex"
	"fmt"
)

// ID is a 16-byte OTLP trace id.
type ID [16]byte

// SpanID is an 8-byte OTLP span id.
type SpanID [8]byte

// String returns the lowercase hex form used on the wire and in the API.
func (t ID) String() string { return hex.EncodeToString(t[:]) }

// IsZero reports whether every byte of the id is zero (invalid per OTLP).
func (t ID) IsZero() bool { return t == ID{} }

// String returns the lowercase hex form used on the wire and in the API.
func (s SpanID) String() string { return hex.EncodeToString(s[:]) }

// IsZero reports whether every byte of the id is zero (no span, e.g. a root's parent).
func (s SpanID) IsZero() bool { return s == SpanID{} }

// ParseID parses a 32-character hex trace id.
func ParseID(s string) (ID, error) {
	var id ID
	if err := decodeHexID(id[:], s); err != nil {
		return ID{}, fmt.Errorf("trace: parse trace id: %w", err)
	}
	return id, nil
}

// ParseSpanID parses a 16-character hex span id.
func ParseSpanID(s string) (SpanID, error) {
	var id SpanID
	if err := decodeHexID(id[:], s); err != nil {
		return SpanID{}, fmt.Errorf("trace: parse span id: %w", err)
	}
	return id, nil
}

// IDFromBytes copies b into an ID; b must be 16 bytes long.
func IDFromBytes(b []byte) (ID, error) {
	var id ID
	if len(b) != len(id) {
		return ID{}, fmt.Errorf("%w: trace id has %d bytes, want %d", ErrInvalidID, len(b), len(id))
	}
	copy(id[:], b)
	return id, nil
}

// SpanIDFromBytes copies b into a SpanID; b must be 8 bytes long, or empty for no span.
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
