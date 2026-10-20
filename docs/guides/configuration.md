# Configuration

Loupe reads one YAML file, then applies environment overrides. Every key below
also appears, with comments, in `deploy/config.example.yaml`. Check a file with
`loupe config validate` and see the merged result with
`loupe config print [--redact]`.

## How configuration is loaded

- File: `--config <path>` or `LOUPE_CONFIG`. With neither, built-in defaults are used.
- Environment: every scalar key can be overridden with `LOUPE_<PATH_IN_UPPER_SNAKE>`,
  for example `receiver.grpc.listen` is `LOUPE_RECEIVER_GRPC_LISTEN`. Environment wins over the file.
- `content.redact` is overridden with a comma-separated list of preset names (`LOUPE_CONTENT_REDACT=email,api_key`).
- Durations are Go strings plus a `d` suffix (`30s`, `14d`). Sizes are bytes with a suffix (`64MiB`).
- Secrets are never set inline; use the `*_file` keys.
- Validation reports every problem at once and the process exits with code 2.

## Receiver

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `receiver.grpc.listen` | `127.0.0.1:4317` | `LOUPE_RECEIVER_GRPC_LISTEN` | OTLP/gRPC listen address. |
| `receiver.grpc.tls.cert_file` | empty | `LOUPE_RECEIVER_GRPC_TLS_CERT_FILE` | TLS certificate; set together with the key. |
| `receiver.grpc.tls.key_file` | empty | `LOUPE_RECEIVER_GRPC_TLS_KEY_FILE` | TLS private key. |
| `receiver.http.listen` | `127.0.0.1:4318` | `LOUPE_RECEIVER_HTTP_LISTEN` | OTLP/HTTP listen address. |
| `receiver.http.tls.cert_file` | empty | `LOUPE_RECEIVER_HTTP_TLS_CERT_FILE` | TLS certificate. |
| `receiver.http.tls.key_file` | empty | `LOUPE_RECEIVER_HTTP_TLS_KEY_FILE` | TLS private key. |
| `receiver.tokens_file` | empty | `LOUPE_RECEIVER_TOKENS_FILE` | Bearer tokens accepted by the receivers, one per line. |
| `receiver.allow_unauthenticated` | `false` | `LOUPE_RECEIVER_ALLOW_UNAUTHENTICATED` | Allow a non-loopback receiver without tokens. |
| `receiver.max_request_bytes` | `16MiB` | `LOUPE_RECEIVER_MAX_REQUEST_BYTES` | Request size limit, applied after decompression. |
| `receiver.max_spans_per_request` | `10000` | `LOUPE_RECEIVER_MAX_SPANS_PER_REQUEST` | Larger requests are rejected (`too_many_spans`). |
| `receiver.max_attribute_bytes` | `64KiB` | `LOUPE_RECEIVER_MAX_ATTRIBUTE_BYTES` | Per non-content attribute value; longer values are truncated. |
| `receiver.max_inflight_bytes` | `64MiB` | `LOUPE_RECEIVER_MAX_INFLIGHT_BYTES` | Decompressed request bytes in flight across receivers. |

## Dashboard, API and admin

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `ui.listen` | `127.0.0.1:3000` | `LOUPE_UI_LISTEN` | Dashboard, REST API `/v1` and admin API `/v1/admin`. |
| `ui.tls.cert_file` | empty | `LOUPE_UI_TLS_CERT_FILE` | TLS certificate. |
| `ui.tls.key_file` | empty | `LOUPE_UI_TLS_KEY_FILE` | TLS private key. |
| `ui.allow_unauthenticated` | `false` | `LOUPE_UI_ALLOW_UNAUTHENTICATED` | Allow a non-loopback UI without an API token. |
| `ui.session_ttl` | `12h` | `LOUPE_UI_SESSION_TTL` | Dashboard session lifetime. |
| `ui.secure_cookie` | `false` | `LOUPE_UI_SECURE_COOKIE` | Mark the session cookie Secure; always on when `ui.tls` is set. |
| `api.token_file` | empty | `LOUPE_API_TOKEN_FILE` | API bearer token(s), one per line. |
| `api.generate_token` | `false` | `LOUPE_API_GENERATE_TOKEN` | Create `api.token_file` (0600, 256-bit hex) on start if missing; needs `api.token_file`. |
| `admin.listen` | `127.0.0.1:9092` | `LOUPE_ADMIN_LISTEN` | `/healthz`, `/readyz`, `/metrics`, optional pprof. |
| `debug.pprof` | `false` | `LOUPE_DEBUG_PPROF` | Serve `/debug/pprof/*` on the admin listener. |

## Storage and runtime

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `storage.path` | `./loupe.db` | `LOUPE_STORAGE_PATH` | SQLite file; one process owns it. |
| `storage.backup_before_migrate` | `true` | `LOUPE_STORAGE_BACKUP_BEFORE_MIGRATE` | Write `<path>.pre-v<N>` before a pending migration. |
| `runtime.memory_limit` | `640MiB` | `LOUPE_RUNTIME_MEMORY_LIMIT` | Go soft memory limit (GOMEMLIMIT) unless `GOMEMLIMIT` is set; `0` disables. |
| `shutdown_timeout` | `30s` | `LOUPE_SHUTDOWN_TIMEOUT` | Time allowed to drain the queue and HTTP servers. |

## Ingest and writer

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `ingest.queue_spans` | `100000` | `LOUPE_INGEST_QUEUE_SPANS` | Queue bound in spans. |
| `ingest.queue_bytes` | `256MiB` | `LOUPE_INGEST_QUEUE_BYTES` | Queue bound in estimated bytes. |
| `ingest.overflow` | `reject` | `LOUPE_INGEST_OVERFLOW` | When full: `reject` (429 / RESOURCE_EXHAUSTED) or `drop`. |
| `ingest.sync_ack` | `false` | `LOUPE_INGEST_SYNC_ACK` | Reply only after the batch commits. |
| `writer.batch_spans` | `2000` | `LOUPE_WRITER_BATCH_SPANS` | Spans per write transaction. |
| `writer.batch_interval` | `200ms` | `LOUPE_WRITER_BATCH_INTERVAL` | Flush a partial batch after this long. |

## Content and retention

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `content.capture` | `off` | `LOUPE_CONTENT_CAPTURE` | `off`, `on` or `metadata_only`. |
| `content.max_bytes` | `32KiB` | `LOUPE_CONTENT_MAX_BYTES` | Stored content text limit per span (after redaction). |
| `content.redact` | `[email, api_key]` | `LOUPE_CONTENT_REDACT` | Presets or `{name, pattern, replacement}` rules. |
| `retention.spans` | `14d` | `LOUPE_RETENTION_SPANS` | Span age limit. |
| `retention.content` | `7d` | `LOUPE_RETENTION_CONTENT` | Content age limit (not above `spans`). |
| `retention.rollups_hourly` | `35d` | `LOUPE_RETENTION_ROLLUPS_HOURLY` | Hourly rollup age limit. |
| `retention.rollups_daily` | `400d` | `LOUPE_RETENTION_ROLLUPS_DAILY` | Daily rollup age limit. |
| `retention.orphan_content` | `1h` | `LOUPE_RETENTION_ORPHAN_CONTENT` | Age limit for content with no stored span. |
| `retention.max_db_bytes` | `10GiB` | `LOUPE_RETENTION_MAX_DB_BYTES` | Disk cap (main + WAL + SHM); `0` = off, otherwise at least 64 MiB. |
| `retention.interval` | `10m` | `LOUPE_RETENTION_INTERVAL` | Time between retention passes. |
| `retention.batch` | `5000` | `LOUPE_RETENTION_BATCH` | Rows per delete job. |

## Other

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `pricing.file` | empty | `LOUPE_PRICING_FILE` | User price table merged over the embedded default. |
| `self_tracing.endpoint` | empty | `LOUPE_SELF_TRACING_ENDPOINT` | Reserved for exporting Loupe's own traces. Not implemented in v0.1: the value is validated, `config validate` warns, and nothing is sent. |
| `log.level` | `info` | `LOUPE_LOG_LEVEL` | `debug`, `info`, `warn`, `error`. |
| `log.format` | `json` | `LOUPE_LOG_FORMAT` | `json` or `text`. |
| `log.access_log` | `false` | `LOUPE_LOG_ACCESS_LOG` | Log every HTTP request. |

## Validation rules worth knowing

- The four listen addresses must not collide on a port.
- A non-loopback receiver listener needs `receiver.tokens_file` or `receiver.allow_unauthenticated: true`.
- A non-loopback `ui.listen` needs `api.token_file` or `ui.allow_unauthenticated: true`.
- A TLS cert and key must be set together.
- Sizes, counts, `session_ttl` and `shutdown_timeout` must be positive; `ingest.overflow`, `content.capture`, `log.level` and `log.format` must be one of the listed values.

See [operations.md](operations.md) for how these settings behave in production.
