# Operations

Running Loupe: deploy, secure, back up, upgrade, monitor and maintain it. Every
option is listed in [configuration.md](configuration.md).

## Deployment

Loupe is one binary and one SQLite file. Run exactly one process per database.

### Binary

```sh
loupe serve --config loupe.yaml
```

With no config it listens on loopback only and writes `./loupe.db`.

### Container image

The image is distroless, runs as user 65532, keeps data in `VOLUME /data` and
has a `HEALTHCHECK` that runs `loupe healthcheck`. Its environment defaults are:

| Variable | Value |
|---|---|
| `LOUPE_STORAGE_PATH` | `/data/loupe.db` |
| `LOUPE_RECEIVER_GRPC_LISTEN` | `0.0.0.0:4317` |
| `LOUPE_RECEIVER_HTTP_LISTEN` | `0.0.0.0:4318` |
| `LOUPE_UI_LISTEN` | `0.0.0.0:3000` |
| `LOUPE_RECEIVER_ALLOW_UNAUTHENTICATED` | `true` |
| `LOUPE_API_TOKEN_FILE` | `/data/api_token` |
| `LOUPE_API_GENERATE_TOKEN` | `true` |

Publish ports on the host loopback only:

```sh
docker run -d --name loupe \
  -p 127.0.0.1:3000:3000 -p 127.0.0.1:4318:4318 \
  -v loupe:/data ghcr.io/thnhdatle/loupe:v0.1.0
docker cp loupe:/data/api_token .
```

The image leaves `admin.listen` at `127.0.0.1:9092`, which is inside the
container: the health check works, but `/metrics` is not reachable from outside.
To scrape it, set `LOUPE_ADMIN_LISTEN=0.0.0.0:9092` and publish the port on a
trusted network only. The admin listener has no authentication.

The receivers accept unauthenticated traffic in the image, which is safe only
because the ports are published on loopback. Before exposing them, mount a tokens
file, set `LOUPE_RECEIVER_TOKENS_FILE` and set
`LOUPE_RECEIVER_ALLOW_UNAUTHENTICATED=false`.

### Docker Compose

`deploy/docker-compose.yml` sets the same environment explicitly, adds
`LOUPE_ADMIN_LISTEN=0.0.0.0:9092`, mounts a `loupe-data` volume, publishes
3000, 4317, 4318 and 9092 on `127.0.0.1`, and uses `loupe healthcheck` as its
health check. `docker compose -f deploy/docker-compose.yml up` starts it.

### Kubernetes

Apply the manifests in `deploy/k8s` (`kubectl apply -n loupe -k deploy/k8s`; see
its README). They run a single-replica StatefulSet with the volume at `/data`,
because two pods must never open the same database, and use exec probes
(`loupe healthcheck`) because the distroless image has no shell or curl.

`loupe healthcheck` loads the same config and environment as the server, calls
the admin `/readyz` with a 3 second timeout and exits 0 only on HTTP 200.

## Listeners and ports

| Listener | Default | Serves |
|---|---|---|
| `receiver.grpc.listen` | `127.0.0.1:4317` | OTLP/gRPC |
| `receiver.http.listen` | `127.0.0.1:4318` | OTLP/HTTP (`/v1/traces`, `/v1/logs`, `/v1/metrics`) |
| `ui.listen` | `127.0.0.1:3000` | dashboard, REST API `/v1`, admin API `/v1/admin` |
| `admin.listen` | `127.0.0.1:9092` | `/healthz`, `/readyz`, `/metrics`, optional `/debug/pprof/*` |

All defaults are loopback. Startup fails if two listeners collide on a port.

## TLS and reverse proxy

Each of the gRPC receiver, HTTP receiver and UI listeners can terminate TLS
itself (`*.tls.cert_file` and `*.tls.key_file`, both or neither). The admin
listener never uses TLS. Certificates are read at start; restart to reload.

A non-loopback receiver or UI listener without TLS logs a warning at startup.
The recommended setup is a TLS-terminating reverse proxy in front of loopback
listeners. Forwarding headers are not trusted by Loupe.

Caddy:

```
loupe.example.com {
    reverse_proxy 127.0.0.1:3000
}
otlp.example.com {
    reverse_proxy 127.0.0.1:4318
}
```

nginx (UI and OTLP/HTTP; gRPC needs `grpc_pass` in a `http2` server):

```nginx
server {
    listen 443 ssl;
    server_name loupe.example.com;
    ssl_certificate     /etc/ssl/loupe.crt;
    ssl_certificate_key /etc/ssl/loupe.key;
    client_max_body_size 16m;          # receiver.max_request_bytes
    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
    }
}
```

When a proxy terminates TLS for the UI, set `ui.secure_cookie: true` so the
session cookie carries `Secure` (it is forced on when `ui.tls` is set).

## Authentication

- **Receivers.** `receiver.tokens_file` holds one token per line. Clients send
  `Authorization: Bearer <token>` or `X-Loupe-Token`. A non-loopback receiver
  listener without tokens is refused at startup unless
  `receiver.allow_unauthenticated: true`.
- **API and admin API.** `api.token_file` holds the bearer tokens for `/v1` and
  `/v1/admin`. A non-loopback `ui.listen` needs it unless
  `ui.allow_unauthenticated: true`. Without a token the admin API is served only
  when `ui.listen` is loopback; on a non-loopback listener it is disabled and a
  warning is logged.
- **Generated token.** With `api.generate_token: true` and a missing
  `api.token_file`, Loupe writes a random 256-bit hex token (mode 0600) at start.
  The token is never logged.
- **Dashboard login.** With `api.generate_token: true`, each start logs a
  single-use `http(s)://<host>:<port>/login?code=...` URL (unspecified hosts
  become `localhost`). The dashboard asks for a login when tokens are set and
  `ui.listen` is not loopback.
- Token files with no tokens are rejected, so a typo cannot disable auth.

## Storage

- The database is one SQLite file in WAL mode: `loupe.db`, `loupe.db-wal`,
  `loupe.db-shm`. Back up with `loupe db backup`, not by copying files.
- The process holds an exclusive `flock` on `<path>.lock` for its lifetime. A
  second `serve` or any offline writing command on the same file fails with
  "database is owned by another process". The kernel releases the lock on exit
  or crash.
- Do not put the file on a network filesystem.
- Inside `serve`, one goroutine writes. Ingest, retention, vacuum and pricing
  recompute all go through it.

## Backup

Online and read-only, safe while the server runs:

```sh
loupe db backup /backups/loupe-2026-10-19.db
```

It uses `VACUUM INTO` and refuses to overwrite an existing file.

Through the running server, with the API token:

```sh
curl -X POST -H "Authorization: Bearer $(cat api_token)" http://127.0.0.1:3000/v1/admin/backups
curl -H "Authorization: Bearer $(cat api_token)" http://127.0.0.1:3000/v1/admin/jobs/<job_id>
```

This writes `backups/loupe-<UTC timestamp>.db` in the directory that holds the
database (`/data/backups/` in the image). Loupe never deletes these files.

Before a migration, Loupe writes `<storage.path>.pre-v<N>` (see Upgrade).
Restore by stopping the server and putting the backup file at `storage.path`
(remove stale `-wal` and `-shm` files). Backups contain captured content;
user deletion does not reach them (see [privacy.md](privacy.md)).

## Upgrade

1. Preview: `loupe db migrate --dry-run` prints current and target versions and
   pending migrations; it is allowed while the server runs.
2. Stop the old process, start the new binary or image. Pending migrations are
   applied at start.
3. With `storage.backup_before_migrate: true` (default), a database above schema
   version 0 first gets `VACUUM INTO <storage.path>.pre-v<target>`. If free disk
   is below the database size, startup fails instead of migrating.
4. To migrate without serving, stop the server and run `loupe db migrate`. It is
   offline only and fails while a server holds the lock.

Downgrades are not supported. A binary refuses a database whose schema is newer
than its own. To go back, stop the server and restore the `.pre-v<N>` file (or
another backup) taken before the upgrade, which discards data written since.

## Memory

`runtime.memory_limit` (default `640MiB`) sets the Go soft memory limit at
start unless the `GOMEMLIMIT` environment variable is set; `0` disables it.
The queue (`ingest.queue_bytes`, 256 MiB) and in-flight request bytes
(`receiver.max_inflight_bytes`, 64 MiB) are bounded separately. Size container
limits above `memory_limit` with headroom: a soft limit makes the collector work
harder but does not stop the process from exceeding it. If you lower
`ingest.queue_bytes` or `max_inflight_bytes`, lower `memory_limit` accordingly.

## Queue, overflow and acknowledgement

- Receivers reply after enqueue by default. Spans in the queue, including the
  batch being written, are lost if the process is killed; the bound is
  `ingest.queue_spans`. A graceful stop drains the queue.
- `ingest.sync_ack: true` makes each response wait for the commit. Nothing
  acknowledged is lost, at lower throughput.
- When the queue is full, `ingest.overflow: reject` (default) answers HTTP 429
  with `Retry-After: 1` or gRPC `RESOURCE_EXHAUSTED` with a 1 second RetryInfo, so
  exporters retry. `drop` answers 200 with `partial_success.rejected_spans` and
  counts the spans as dropped: nothing retries, so data is lost.
- A batch that fails to write is retried with jittered backoff (5 attempts), then
  dropped and counted as `dropped{reason="store_error"}`.
- Larger `writer.batch_spans` raises throughput; larger `writer.batch_interval`
  raises latency until data is visible.

## Retention and disk cap

The planner runs every `retention.interval` and deletes in `retention.batch`
sized jobs interleaved with ingest, then runs an incremental vacuum and a
`wal_checkpoint(TRUNCATE)`. `retention.max_db_bytes` (default 10 GiB; main + WAL +
SHM) deletes the oldest content, then the oldest spans, when exceeded; it is
logged at warn and counted. If only the last hour remains and the cap is still
exceeded, Loupe logs an error and keeps ingesting. Startup warns when free disk
is below twice the cap. Details: [retention.md](retention.md).

## Monitoring

Admin listener endpoints:

- `/healthz`: process is up.
- `/readyz`: 200 when migrations are applied, the writer accepts work and
  shutdown has not begun; 503 otherwise.
- `/metrics`: Prometheus text.
- `/debug/pprof/*`: only with `debug.pprof: true`. Keep it off in production
  unless debugging and never expose the admin port publicly.

Metrics (no Go runtime or process collectors are included):

| Metric | Labels | Meaning |
|---|---|---|
| `loupe_ingest_spans_total` | `result` (stored, rejected, dropped, duplicate) | Spans by outcome. |
| `loupe_ingest_rejected_total` | `reason` | Spans and logs rejected. |
| `loupe_ingest_dropped_total` | `reason` | Spans and logs dropped. |
| `loupe_ingest_discarded_total` | `signal`, `reason` | Accepted but not stored (for example metrics). |
| `loupe_ingest_queue_spans` | | Queue slots held. |
| `loupe_ingest_queue_bytes` | | Estimated queue bytes. |
| `loupe_ingest_inflight_bytes` | | Decompressed request bytes in flight. |
| `loupe_writer_batches_total` | | Committed batches. |
| `loupe_writer_retries_total` | | Write attempts after the first. |
| `loupe_writer_jobs_total` | | Writer jobs run. |
| `loupe_writer_job_errors_total` | | Failed writer jobs. |
| `loupe_writer_queued_jobs` | | Jobs waiting. |
| `loupe_writer_batch_duration_seconds` | | Batch commit time (histogram). |
| `loupe_writer_batch_spans` | | Spans per batch (histogram). |
| `loupe_db_size_bytes` | | Main + WAL + SHM size. |
| `loupe_query_duration_seconds` | `endpoint` | API and dashboard query time (histogram). |
| `loupe_retention_deleted_total` | `class` | Rows deleted by age rules. |
| `loupe_retention_cap_deleted_total` | `class` | Rows deleted by the disk cap. |
| `loupe_retention_passes_total` | | Passes completed. |
| `loupe_retention_pass_errors_total` | | Failed passes. |
| `loupe_retention_cap_exceeded_total` | | Passes that found the DB above the cap. |
| `loupe_retention_cap_unmet_total` | | Passes still above the cap with one hour left. |
| `loupe_retention_checkpoint_busy_total` | | Checkpoints that reported busy. |
| `loupe_content_stripped_total` | | Spans and logs whose content was stripped. |
| `loupe_unpriced_spans_total` | `model` | Spans with no price. |
| `loupe_usage_duplicate_spans_total` | `kind` (llm, embedding) | Spans flagged as duplicate usage. |
| `loupe_build_info` | `version`, `commit`, `go_version` | Build information. |

Useful alerts: `rate(loupe_ingest_rejected_total[5m]) > 0` or any
`loupe_ingest_dropped_total` increase (overload); `loupe_ingest_queue_spans`
near `ingest.queue_spans`; `loupe_retention_cap_unmet_total` increasing;
`increase(loupe_retention_pass_errors_total[1h]) > 0`;
`loupe_db_size_bytes` near `retention.max_db_bytes`; `/readyz` failing.

`loupe db stats [--json]` prints file sizes, schema version, row counts and the
span time range; it is read-only and allowed online.

## Maintenance

- **Vacuum.** `loupe db vacuum` asks the running server to run an incremental
  vacuum through its admin API (`--server`, default derived from `ui.listen`;
  token from `api.token_file` or `--token-file`). `loupe db vacuum --offline`
  runs the same job locally while holding the lock (the server must be stopped).
  `loupe db vacuum --offline --full` runs a full `VACUUM`, which needs free disk
  of about the database size and `--offline`.
- **Pricing.** After editing the price file, `loupe pricing recompute --since
  <date> [--until <date>]` reprices stored spans, online through the admin API
  or with `--offline`. `loupe pricing list` shows the effective table. See
  [pricing.md](pricing.md).
- **Export.** `loupe export [--from] [--to] [--out file]` writes JSON Lines; it is read-only and allowed online.

## Shutdown

On SIGINT or SIGTERM `/readyz` turns 503, the receivers stop accepting new
requests, admin jobs stop, the writer drains its queue, the HTTP servers drain,
and a final `wal_checkpoint(TRUNCATE)` runs before the lock is released. The
whole sequence is bounded by `shutdown_timeout` (default 30s); set an orchestrator
grace period above it. A kill -9 loses only the unacknowledged queue contents;
SQLite recovers the WAL at the next start.
