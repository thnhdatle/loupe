// SPDX-License-Identifier: Apache-2.0

// Package query defines filters, pages, cursors and result summaries used by the read side.
//
// Usage:
//
//	from, to, err := query.ResolveRange(nil, nil, now)
//	limit, err := query.NormalizeLimit(0) // 50
//	cur := query.EncodeCursor(query.TraceCursor(startNs, id), query.SortStart, query.OrderDesc)
//	f := query.TraceFilter{From: from, To: to, Span: query.SpanFilter{Model: "gpt-4o"}}
//	err = f.Validate()
//	_ = cur
package query
