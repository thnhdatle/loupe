// SPDX-License-Identifier: Apache-2.0

package httpmw

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"net/http"
	"time"

	"github.com/thnhdatle/loupe/internal/platform/clock"
)

// RequestIDHeader is the header carrying the request id.
const RequestIDHeader = "X-Request-Id"

const (
	maxRequestIDLen = 64
	crockford       = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

type requestIDKey struct{}

// RequestIDFrom returns the request id stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// ValidRequestID reports whether s is acceptable as a client-supplied request
// id: 1 to 64 characters from [A-Za-z0-9._-].
func ValidRequestID(s string) bool {
	if s == "" || len(s) > maxRequestIDLen {
		return false
	}
	for i := range len(s) {
		c := s[i]
		ok := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// NewULID returns a 26-character ULID for time t using crypto/rand.
func NewULID(t time.Time) string {
	var b [16]byte
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(t.UnixMilli()))
	copy(b[:6], ts[2:])
	// A failed read leaves the random part zero; ids stay well formed.
	_, _ = rand.Read(b[6:])
	var out [26]byte
	// 128 bits are encoded as 26 groups of 5 bits, most significant first; the
	// first group carries only 3 bits.
	var acc uint
	bits := 0
	pos := 25
	for i := 15; i >= 0; i-- {
		acc |= uint(b[i]) << bits
		bits += 8
		for bits >= 5 && pos >= 0 {
			out[pos] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			pos--
		}
	}
	if pos >= 0 {
		out[pos] = crockford[acc&31]
	}
	return string(out[:])
}

// RequestID accepts a valid X-Request-Id or generates a ULID, stores it in the
// request context and echoes it in the response.
func RequestID(clk clock.Clock) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !ValidRequestID(id) {
				id = NewULID(clk.Now())
			}
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
		})
	}
}
