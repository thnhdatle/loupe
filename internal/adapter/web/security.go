// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const cspFormat = "default-src 'self'; script-src 'self' 'nonce-%s'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

const nonceBytes = 16

type nonceKey struct{}

func (h *Handler) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := h.rnd.read(nonceBytes)
		if err != nil {
			h.log.Error("nonce", "component", "web", "error", err.Error())
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		nonce := base64.RawStdEncoding.EncodeToString(raw)
		hd := w.Header()
		hd.Set("Content-Security-Policy", fmt.Sprintf(cspFormat, nonce))
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce)))
	})
}

func nonceOf(ctx context.Context) string {
	n, _ := ctx.Value(nonceKey{}).(string)
	return n
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	if o == "null" {
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	u, err := url.Parse(o)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

func isNotFound(err error) bool {
	return errors.Is(err, trace.ErrNotFound) || errors.Is(err, trace.ErrInvalidID)
}

func isInvalid(err error) bool {
	for _, target := range []error{
		query.ErrInvalidCursor, query.ErrInvalidLimit, query.ErrInvalidRange,
		query.ErrInvalidUSD, query.ErrInvalidSort, query.ErrInvalidFilter,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
