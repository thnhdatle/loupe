// SPDX-License-Identifier: Apache-2.0

// Package load is an open-model OTLP load generator for the benchmarks.
//
// Requests are scheduled at a constant arrival rate regardless of responses, sent
// exactly once (no retries), and split between OTLP/HTTP protobuf and OTLP/gRPC.
// Spans count as sent only when their request got a
// response, and transport errors are reported separately.
//
// Usage:
//
//	sum, err := load.Run(ctx, load.Config{
//		HTTPURL: "http://127.0.0.1:4318", Rate: 3000, Duration: time.Minute,
//	})
package load
