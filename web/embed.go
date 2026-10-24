// SPDX-License-Identifier: Apache-2.0

// Package web embeds the dashboard templates and static files.
package web

import "embed"

// FS holds the dashboard templates and static files.
//
//go:embed templates static
var FS embed.FS
