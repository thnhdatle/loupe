# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-21

First public release.

### Added
- Single binary `loupe` with one SQLite file (WAL, single writer, exclusive file lock), forward-only migrations with an automatic `.pre-v<N>` copy, and no external services.
- OTLP receivers over gRPC (`:4317`) and HTTP (`:4318`) for traces and GenAI log events; bearer tokens, TLS, request and attribute size limits; OTLP metrics are accepted and discarded.
- Semantic-convention translation for `gen_ai`, OpenInference and legacy attributes, with order-independent usage de-duplication.
- Cost and token accounting in exact decimal arithmetic with a price table, a user price file and `loupe pricing list` / `recompute`.
- Rollups with log-bucket percentiles, per model, user and hour or day.
- Ingest pipeline: content policy and redaction presets (capture `off` by default; `content.max_bytes` per span across attributes, events and GenAI log events), bounded queue by spans and bytes, `reject` or `drop` on overflow, optional `sync_ack`, in-flight byte limiter.
- Single writer: size and interval flush, jittered bounded retries then drop-and-count, slot release at commit, graceful drain, serial job execution.
- REST API `/v1` with keyset pagination, filters, ranges and a stable error registry; `GET /v1/export/traces` JSON Lines export; `api/openapi.yaml` (OpenAPI 3.1) with contract tests.
- Admin API and CLI: `loupe healthcheck`, `db stats`, `db vacuum` (online, or `--offline [--full]`), `db backup <path>`, `db migrate [--dry-run]`, `pricing list` and `pricing recompute` (online or `--offline`), `config validate` and `config print [--redact]`, `export [--from --to --out --format jsonl]` (read-only, works while the server runs).
- Embedded dashboard (`/`): Overview, Traces, Trace detail, Sessions and Models pages, server-rendered with vendored htmx and uPlot (no external requests, strict CSP), session login and a single-use login URL logged at start with `api.generate_token`.
- Retention planner (age rules per data class) and a disk cap on main file, WAL and SHM, with incremental vacuum and WAL checkpoint.
- Prometheus metrics and `/healthz`, `/readyz`, optional pprof on the admin listener; JSON logs with a fixed key vocabulary.
- Distroless non-root container image, Docker Compose file, Kubernetes manifests (`deploy/k8s`), example configuration.
- Examples for OpenTelemetry and OpenInference SDKs and Triage with recorded payloads; a quickstart test.
- Guides: quickstart, configuration, operations, troubleshooting, errors, instrumentation, pricing, privacy, retention.
- Measured envelope: 3,000 spans/s sustained with 0 lost, 15,000 spans/s single-writer capacity, query p95 0.058 s at 2M spans.

[Unreleased]: https://github.com/thnhdatle/loupe/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/thnhdatle/loupe/releases/tag/v0.1.0
