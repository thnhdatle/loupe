// SPDX-License-Identifier: Apache-2.0

package httpmw

import (
	"net/http"
	"sort"
	"strings"
)

// Router is a ServeMux that answers unknown paths with a JSON 404 and known
// paths under another method with a JSON 405 carrying an Allow header.
type Router struct {
	mux     *http.ServeMux
	methods map[string][]string
	routes  []string
}

// NewRouter returns an empty Router.
func NewRouter() *Router {
	r := &Router{mux: http.NewServeMux(), methods: make(map[string][]string)}
	r.mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		WriteError(w, req, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return r
}

// Handle registers fn for method and path (a Go 1.22 path pattern).
func (r *Router) Handle(method, path string, fn http.HandlerFunc) {
	r.mux.HandleFunc(method+" "+path, fn)
	if len(r.methods[path]) == 0 {
		r.mux.HandleFunc(path, func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Allow", strings.Join(r.methods[path], ", "))
			WriteError(w, req, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		})
	}
	r.routes = append(r.routes, method+" "+path)
	r.methods[path] = append(r.methods[path], method)
	sort.Strings(r.methods[path])
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }

// Routes returns the registered routes as "METHOD /path" in registration order.
func (r *Router) Routes() []string { return append([]string(nil), r.routes...) }
