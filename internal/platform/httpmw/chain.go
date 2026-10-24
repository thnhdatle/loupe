// SPDX-License-Identifier: Apache-2.0

package httpmw

import "net/http"

type Middleware func(http.Handler) http.Handler

// Chain wraps h with the middlewares, the first being the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
