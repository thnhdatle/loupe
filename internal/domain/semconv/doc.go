// SPDX-License-Identifier: Apache-2.0

// Package semconv translates raw OTLP spans and log records into domain spans
// using the GenAI semantic convention tables (alias table, kind rules and
// content-key denylist; see docs/guides/instrumentation.md and privacy.md).
//
// Usage:
//
//	span := semconv.Translate(raw, semconv.Options{MaxAttrBytes: 64 << 10})
//	parts, reason, keep := semconv.TranslateLog(rec)
package semconv
