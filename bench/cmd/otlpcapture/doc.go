// SPDX-License-Identifier: Apache-2.0

// Command otlpcapture is a tiny OTLP/HTTP capture server used by
// scripts/record_otlp.sh. It accepts protobuf or JSON export requests on
// /v1/traces, /v1/logs and /v1/metrics and writes each trace and log request as a
// normalised OTLP/JSON file (hex ids, indented) named <prefix>traces-N.json or
// <prefix>logs-N.json in the output directory. Metrics are acknowledged and dropped.
//
// Usage:
//
//	go run ./bench/cmd/otlpcapture -dir testdata/otlp/openai-v2 -prefix recorded-events- -addr 127.0.0.1:14318
package main
