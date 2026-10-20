// SPDX-License-Identifier: Apache-2.0

// Package web serves the embedded server-rendered dashboard: the
// Overview, Traces, Trace detail, Sessions and Models pages, the login and
// logout flow with in-memory sessions and first-start login codes, and the
// security headers (CSP with a per-response nonce).
//
// Templates and static assets live in the top-level web package and are
// embedded in the binary.
//
// Usage:
//
//	h, err := web.NewHandler(queriesService, web.Options{
//		Tokens: tokens, RequireLogin: true, SecureCookie: tls,
//	})
//	mux.Handle("/", h)
//	api := httpapi.Options{SessionAuth: h.SessionAuth}
package web
