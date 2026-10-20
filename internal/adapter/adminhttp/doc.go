// SPDX-License-Identifier: Apache-2.0

// Package adminhttp serves the mutating admin API under /v1/admin:
// pricing recompute, vacuum, backups and job status. It runs as its own
// handler with its own bearer-token authentication: only API tokens are
// accepted, never a dashboard session cookie.
//
// Usage:
//
//	h, err := adminhttp.NewHandler(adminSvc, adminhttp.Options{Tokens: tokens, StorageDir: dir, Calculator: calc})
//	srv := httpmw.NewServer(addr, h, nil)
package adminhttp
