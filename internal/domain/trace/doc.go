// SPDX-License-Identifier: Apache-2.0

// Package trace defines the span, trace and raw OTLP value types and the ingest and storage ports.
//
// Usage:
//
//	var _ trace.Ingestor // consumed by the OTLP adapter; implemented by app/ingest
package trace
