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

// NewBearer returns a Bearer accepting any of tokens; empty tokens are ignored.
func NewBearer(tokens []string) *Bearer {
	b := &Bearer{}
	for _, t := range tokens {
		if t = strings.TrimSpace(t); t != "" {
			b.digests = append(b.digests, sha256.Sum256([]byte(t)))
		}
	}
	return b
}

// Enabled reports whether any token is configured.
func (b *Bearer) Enabled() bool { return len(b.digests) > 0 }

// Check reports whether the Authorization header carries a configured token.
// Tokens are hashed first so timing does not depend on length, and every
// configured token is compared so it does not depend on which one matched.
// With no token configured it returns false.
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

// Unauthorized writes the 401 envelope with a Bearer challenge.
func Unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="loupe"`)
	WriteError(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
}
