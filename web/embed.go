// SPDX-License-Identifier: Apache-2.0

// Package web holds the embedded dashboard assets: HTML templates and static
// files (CSS, vendored htmx and uPlot, app.js). It lives at the repository top
// level because go:embed cannot reach outside the package directory; the
// handlers that use it are in internal/adapter/web.
package web

import "embed"

// FS is the embedded dashboard tree: "templates/*.html" and "static/**".
//
//go:embed templates static
var FS embed.FS
