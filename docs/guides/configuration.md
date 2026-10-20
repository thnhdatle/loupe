# Configuration

Set Loupe's options in a YAML file or with environment variables.

```sh
loupe serve --config loupe.yaml   # or LOUPE_CONFIG; built-in defaults without either
loupe config validate             # check a file
loupe config print --redact       # show the merged result
```

`deploy/config.example.yaml` lists every key with comments.

## How configuration is loaded

- Override any scalar key with `LOUPE_<PATH_IN_UPPER_SNAKE>`, e.g. `LOUPE_RECEIVER_GRPC_LISTEN`. Environment wins over the file.
- `LOUPE_CONTENT_REDACT` takes a comma-separated preset list: `email,api_key`.
- Durations: `30s`, `14d`. Sizes: `64MiB`.
- Never put secrets inline. Use the `*_file` keys.
- Invalid config lists every problem and exits with code 2.

## Receiver

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `receiver.grpc.listen` | `127.0.0.1:4317` | `LOUPE_RECEIVER_GRPC_LISTEN` | OTLP/gRPC address. |
| `receiver.grpc.tls.cert_file` | empty | `LOUPE_RECEIVER_GRPC_TLS_CERT_FILE` | TLS certificate; set with the key. |
| `receiver.grpc.tls.key_file` | empty | `LOUPE_RECEIVER_GRPC_TLS_KEY_FILE` | TLS private key. |
| `receiver.http.listen` | `127.0.0.1:4318` | `LOUPE_RECEIVER_HTTP_LISTEN` | OTLP/HTTP address. |
| `receiver.http.tls.cert_file` | empty | `LOUPE_RECEIVER_HTTP_TLS_CERT_FILE` | TLS certificate. |
| `receiver.http.tls.key_file` | empty | `LOUPE_RECEIVER_HTTP_TLS_KEY_FILE` | TLS private key. |
| `receiver.tokens_file` | empty | `LOUPE_RECEIVER_TOKENS_FILE` | Receiver bearer tokens, one per line. |
| `receiver.allow_unauthenticated` | `false` | `LOUPE_RECEIVER_ALLOW_UNAUTHENTICATED` | Allow non-loopback without tokens. |
| `receiver.max_request_bytes` | `16MiB` | `LOUPE_RECEIVER_MAX_REQUEST_BYTES` | Request limit, after decompression. |
| `receiver.max_spans_per_request` | `10000` | `LOUPE_RECEIVER_MAX_SPANS_PER_REQUEST` | Above this: `too_many_spans`. |
| `receiver.max_attribute_bytes` | `64KiB` | `LOUPE_RECEIVER_MAX_ATTRIBUTE_BYTES` | Per attribute value; longer is truncated. |
| `receiver.max_inflight_bytes` | `64MiB` | `LOUPE_RECEIVER_MAX_INFLIGHT_BYTES` | Request bytes in flight, all receivers. |

## Dashboard, API and admin

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `ui.listen` | `127.0.0.1:3000` | `LOUPE_UI_LISTEN` | Dashboard, `/v1` and `/v1/admin`. |
| `ui.tls.cert_file` | empty | `LOUPE_UI_TLS_CERT_FILE` | TLS certificate. |
| `ui.tls.key_file` | empty | `LOUPE_UI_TLS_KEY_FILE` | TLS private key. |
| `ui.allow_unauthenticated` | `false` | `LOUPE_UI_ALLOW_UNAUTHENTICATED` | Allow non-loopback without API token. |
| `ui.session_ttl` | `12h` | `LOUPE_UI_SESSION_TTL` | Dashboard session lifetime. |
| `ui.secure_cookie` | `false` | `LOUPE_UI_SECURE_COOKIE` | Secure session cookie; on with `ui.tls`. |
| `api.token_file` | empty | `LOUPE_API_TOKEN_FILE` | API bearer tokens, one per line. |
| `api.generate_token` | `false` | `LOUPE_API_GENERATE_TOKEN` | Create `api.token_file` if missing; needs `api.token_file`. |
| `admin.listen` | `127.0.0.1:9092` | `LOUPE_ADMIN_LISTEN` | `/healthz`, `/readyz`, `/metrics`, pprof. |
| `debug.pprof` | `false` | `LOUPE_DEBUG_PPROF` | Serve `/debug/pprof/*` on admin. |

## Storage and runtime

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `storage.path` | `./loupe.db` | `LOUPE_STORAGE_PATH` | SQLite file; one process only. |
| `storage.backup_before_migrate` | `true` | `LOUPE_STORAGE_BACKUP_BEFORE_MIGRATE` | Write `<path>.pre-v<N>` before migrating. |
| `runtime.memory_limit` | `640MiB` | `LOUPE_RUNTIME_MEMORY_LIMIT` | Go soft memory limit unless `GOMEMLIMIT` is set; `0` disables. |
| `shutdown_timeout` | `30s` | `LOUPE_SHUTDOWN_TIMEOUT` | Time to drain on shutdown. |

## Ingest and writer

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `ingest.queue_spans` | `100000` | `LOUPE_INGEST_QUEUE_SPANS` | Queue size in spans. |
| `ingest.queue_bytes` | `256MiB` | `LOUPE_INGEST_QUEUE_BYTES` | Queue size in bytes. |
| `ingest.overflow` | `reject` | `LOUPE_INGEST_OVERFLOW` | When full: `reject` (429) or `drop`. |
| `ingest.sync_ack` | `false` | `LOUPE_INGEST_SYNC_ACK` | Reply after commit. |
| `writer.batch_spans` | `2000` | `LOUPE_WRITER_BATCH_SPANS` | Spans per write. |
| `writer.batch_interval` | `200ms` | `LOUPE_WRITER_BATCH_INTERVAL` | Max wait before a write. |

## Content and retention

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `content.capture` | `off` | `LOUPE_CONTENT_CAPTURE` | `off`, `on` or `metadata_only`. |
| `content.max_bytes` | `32KiB` | `LOUPE_CONTENT_MAX_BYTES` | Stored content per span. |
| `content.redact` | `[email, api_key]` | `LOUPE_CONTENT_REDACT` | Presets or `{name, pattern, replacement}` rules. |
| `retention.spans` | `14d` | `LOUPE_RETENTION_SPANS` | Span age limit. |
| `retention.content` | `7d` | `LOUPE_RETENTION_CONTENT` | Content age limit; not above `spans`. |
| `retention.rollups_hourly` | `35d` | `LOUPE_RETENTION_ROLLUPS_HOURLY` | Hourly rollup age limit. |
| `retention.rollups_daily` | `400d` | `LOUPE_RETENTION_ROLLUPS_DAILY` | Daily rollup age limit. |
| `retention.orphan_content` | `1h` | `LOUPE_RETENTION_ORPHAN_CONTENT` | Age limit for content without a span. |
| `retention.max_db_bytes` | `10GiB` | `LOUPE_RETENTION_MAX_DB_BYTES` | Disk cap; `0` = off, else at least 64 MiB. |
| `retention.interval` | `10m` | `LOUPE_RETENTION_INTERVAL` | Time between passes. |
| `retention.batch` | `5000` | `LOUPE_RETENTION_BATCH` | Rows per delete job. |

## Other

| Key | Default | Env var | Meaning |
|---|---|---|---|
| `pricing.file` | empty | `LOUPE_PRICING_FILE` | Your price table, merged over the default. |
| `self_tracing.endpoint` | empty | `LOUPE_SELF_TRACING_ENDPOINT` | Not implemented in v0.1; nothing is sent. |
| `log.level` | `info` | `LOUPE_LOG_LEVEL` | `debug`, `info`, `warn`, `error`. |
| `log.format` | `json` | `LOUPE_LOG_FORMAT` | `json` or `text`. |
| `log.access_log` | `false` | `LOUPE_LOG_ACCESS_LOG` | Log every HTTP request. |

## Validation rules worth knowing

- Listen addresses must not share a port.
- A non-loopback receiver needs `receiver.tokens_file` or `receiver.allow_unauthenticated: true`.
- A non-loopback `ui.listen` needs `api.token_file` or `ui.allow_unauthenticated: true`.
- Set a TLS cert and key together.
- Sizes, counts, `session_ttl` and `shutdown_timeout` must be positive.
- `ingest.overflow`, `content.capture`, `log.level` and `log.format` accept only the listed values.

See [operations.md](operations.md) for production use.
