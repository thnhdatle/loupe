// SPDX-License-Identifier: Apache-2.0

// Package otlp serves the OTLP gRPC and HTTP receivers and decodes payloads to raw spans.
//
// Traces and logs are converted to trace.RawSpan and trace.RawLogRecord and handed to
// a trace.Ingestor; metrics are decoded only to count data points and then discarded
// (only traces and GenAI log events are stored). Both transports share one Server, which enforces bearer auth, the
// request size limit (after decompression), the span-count limit and the in-flight
// byte limit, and answers overload with HTTP 429 + Retry-After or gRPC
// RESOURCE_EXHAUSTED + RetryInfo.
//
// Usage:
//
//	srv, err := otlp.NewServer(otlp.DefaultConfig(), ingestor, limiter)
//	go srv.RunHTTP(ctx, httpListener)
//	err = srv.RunGRPC(ctx, grpcListener)
package otlp
