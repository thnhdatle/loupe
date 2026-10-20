// SPDX-License-Identifier: Apache-2.0

package httpmw

import "net/http"

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain wraps h so that the first middleware is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
