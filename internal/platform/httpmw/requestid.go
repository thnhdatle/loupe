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

const RequestIDHeader = "X-Request-Id"

const (
	maxRequestIDLen = 64
	crockford       = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

type requestIDKey struct{}

func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

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

func NewULID(t time.Time) string {
	var b [16]byte
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(t.UnixMilli()))
	copy(b[:6], ts[2:])
	_, _ = rand.Read(b[6:])
	var out [26]byte
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

// RequestID reads or generates a request id and stores it on the context and response.
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
