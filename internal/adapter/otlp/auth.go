// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

type authenticator struct {
	digests [][sha256.Size]byte
}

func newAuthenticator(tokens []string) *authenticator {
	a := &authenticator{digests: make([][sha256.Size]byte, len(tokens))}
	for i, t := range tokens {
		a.digests[i] = sha256.Sum256([]byte(t))
	}
	return a
}

func (a *authenticator) enabled() bool { return len(a.digests) > 0 }

func (a *authenticator) check(authorization, xToken string) bool {
	if !a.enabled() {
		return true
	}
	candidates := make([]string, 0, 2)
	if scheme, tok, ok := strings.Cut(authorization, " "); ok && strings.EqualFold(scheme, "bearer") {
		candidates = append(candidates, strings.TrimSpace(tok))
	}
	if xToken != "" {
		candidates = append(candidates, xToken)
	}
	match := 0
	for _, c := range candidates {
		sum := sha256.Sum256([]byte(c))
		for i := range a.digests {
			match |= subtle.ConstantTimeCompare(sum[:], a.digests[i][:])
		}
	}
	return match == 1
}
