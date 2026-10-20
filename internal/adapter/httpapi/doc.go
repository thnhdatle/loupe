// SPDX-License-Identifier: Apache-2.0

// Package httpapi serves the read-only REST /v1 API on the UI
// listener: traces, sessions, statistics, ingest counters, user data deletion
// and the version. It adds bearer-token authentication, the JSON error
// envelope, cursor pagination parameters and request limits.
//
// The admin operations under /v1/admin live in package adminhttp.
//
// Usage:
//
//	h, err := httpapi.NewHandler(queries, deleter, httpapi.Options{Tokens: tokens})
//	srv := httpmw.NewServer(addr, h, nil)
package httpapi
