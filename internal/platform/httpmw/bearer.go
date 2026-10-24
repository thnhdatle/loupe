// SPDX-License-Identifier: Apache-2.0

package httpmw

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Bearer checks bearer tokens in constant time.
type Bearer struct {
	digests [][sha256.Size]byte
}

// NewBearer returns a Bearer accepting any of tokens.
func NewBearer(tokens []string) *Bearer {
	b := &Bearer{}
	for _, t := range tokens {
		if t = strings.TrimSpace(t); t != "" {
			b.digests = append(b.digests, sha256.Sum256([]byte(t)))
		}
	}
	return b
}

func (b *Bearer) Enabled() bool { return len(b.digests) > 0 }

func (b *Bearer) Check(authorization string) bool {
	scheme, tok, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return false
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(tok)))
	match := 0
	for i := range b.digests {
		match |= subtle.ConstantTimeCompare(sum[:], b.digests[i][:])
	}
	return match == 1
}

func Unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="loupe"`)
	WriteError(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
}
