# Operations

Deploy, secure, back up, upgrade and monitor Loupe. Every option is in [configuration.md](configuration.md).

## Deployment

Loupe is one binary and one SQLite file. Run exactly one process per database.
A second one fails with "database is owned by another process". Do not use a network filesystem.

### Binary

```sh
loupe serve --config loupe.yaml   # no config: loopback only, ./loupe.db
```

### Container image

```sh
# Publish ports on the host loopback only.
docker run -d --name loupe \
  -p 127.0.0.1:3000:3000 -p 127.0.0.1:4318:4318 \
  -v loupe:/data ghcr.io/thnhdatle/loupe:v0.1.0
docker cp loupe:/data/api_token .   # copy the API token
```

The image runs as user 65532 and keeps data in `/data`. Its environment defaults:

| Variable | Value |
|---|---|
| `LOUPE_STORAGE_PATH` | `/data/loupe.db` |
| `LOUPE_RECEIVER_GRPC_LISTEN` | `0.0.0.0:4317` |
| `LOUPE_RECEIVER_HTTP_LISTEN` | `0.0.0.0:4318` |
| `LOUPE_UI_LISTEN` | `0.0.0.0:3000` |
| `LOUPE_RECEIVER_ALLOW_UNAUTHENTICATED` | `true` |
| `LOUPE_API_TOKEN_FILE` | `/data/api_token` |
| `LOUPE_API_GENERATE_TOKEN` | `true` |

The receivers accept unauthenticated traffic in the image. This is safe only on loopback.
Before exposing them, mount a tokens file, set `LOUPE_RECEIVER_TOKENS_FILE` and set
`LOUPE_RECEIVER_ALLOW_UNAUTHENTICATED=false`.

`/metrics` is not reachable from outside the container. To scrape it, set
`LOUPE_ADMIN_LISTEN=0.0.0.0:9092` and publish it on a trusted network only.
The admin listener has no authentication.

### Docker Compose

```sh
docker compose -f deploy/docker-compose.yml up
```

Same environment as the image, plus `LOUPE_ADMIN_LISTEN=0.0.0.0:9092`. Ports 3000, 4317, 4318
and 9092 are published on `127.0.0.1`.

### Kubernetes

```sh
kubectl apply -n loupe -k deploy/k8s   # see deploy/k8s/README.md
```

Runs a single-replica StatefulSet. Keep one replica: two pods must never open the same database.

`loupe healthcheck` exits 0 only when the admin `/readyz` returns 200.

## Listeners and ports

| Listener | Default | Serves |
|---|---|---|
| `receiver.grpc.listen` | `127.0.0.1:4317` | OTLP/gRPC |
| `receiver.http.listen` | `127.0.0.1:4318` | OTLP/HTTP (`/v1/traces`, `/v1/logs`, `/v1/metrics`) |
| `ui.listen` | `127.0.0.1:3000` | dashboard, REST API `/v1`, admin API `/v1/admin` |
| `admin.listen` | `127.0.0.1:9092` | `/healthz`, `/readyz`, `/metrics`, optional `/debug/pprof/*` |

All defaults are loopback. Two listeners on one port fail startup.

## TLS and reverse proxy

Set `*.tls.cert_file` and `*.tls.key_file` on the receiver or UI listeners. The admin listener has no TLS.
Restart to reload certificates.

The recommended setup is a TLS reverse proxy in front of loopback listeners. Caddy:

```
loupe.example.com {
    reverse_proxy 127.0.0.1:3000
}
otlp.example.com {
    reverse_proxy 127.0.0.1:4318
}
```

nginx (gRPC needs `grpc_pass` in a `http2` server):

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

Behind a TLS proxy, set `ui.secure_cookie: true`.

## Authentication

| What | Setting | How to authenticate |
|---|---|---|
| Receivers | `receiver.tokens_file`, one token per line | `Authorization: Bearer <token>` or `X-Loupe-Token` |
| API and admin API | `api.token_file` | `Authorization: Bearer <token>` |
| Dashboard | login URL logged at each start with `api.generate_token: true` | open the single-use `/login?code=...` URL |

- A non-loopback receiver without tokens fails startup unless `receiver.allow_unauthenticated: true`.
- A non-loopback `ui.listen` needs `api.token_file` unless `ui.allow_unauthenticated: true`.
- Without a token, the admin API works only on a loopback `ui.listen`.
- `api.generate_token: true` creates a missing `api.token_file` at start. The token is never logged.
- An empty token file is rejected.

## Backup

Back up with these commands, not by copying files.

```sh
loupe db backup /backups/loupe-2026-10-19.db   # online; never overwrites a file
```

Or through the running server:

```sh
curl -X POST -H "Authorization: Bearer $(cat api_token)" http://127.0.0.1:3000/v1/admin/backups
curl -H "Authorization: Bearer $(cat api_token)" http://127.0.0.1:3000/v1/admin/jobs/<job_id>   # job status
```

This writes `backups/loupe-<UTC timestamp>.db` next to the database (`/data/backups/` in the image).
Loupe never deletes these files.

To restore, stop the server and put the backup at `storage.path`. Remove stale `-wal` and `-shm` files.

Backups contain captured content. User deletion does not reach them ([privacy.md](privacy.md)).

## Upgrade

```sh
loupe db migrate --dry-run   # preview pending migrations; works while serving
```

1. Stop the old process.
2. Start the new binary or image. It migrates at start.
3. With `storage.backup_before_migrate: true` (default), it first writes `<storage.path>.pre-v<target>`.
   Startup fails if free disk is below the database size.

To migrate without serving, stop the server and run `loupe db migrate`.

Downgrades are not supported. To go back, stop the server and restore the `.pre-v<N>` file or
another backup. Data written since is lost.

## Memory

`runtime.memory_limit` (default `640MiB`) is a soft limit. `0` disables it; `GOMEMLIMIT` overrides it.
Set container limits above it with headroom. If you lower `ingest.queue_bytes` or `max_inflight_bytes`,
lower `memory_limit` too.

## Queue, overflow and acknowledgement

| Setting | Effect |
|---|---|
| default | reply after enqueue; queued spans are lost if the process is killed |
| `ingest.sync_ack: true` | reply after commit; nothing acknowledged is lost; lower throughput |
| `ingest.overflow: reject` (default) | full queue: HTTP 429 or gRPC `RESOURCE_EXHAUSTED`; exporters retry |
| `ingest.overflow: drop` | full queue: 200, spans dropped, data lost |
| larger `writer.batch_spans` | more throughput |
| larger `writer.batch_interval` | data visible later |

A graceful stop drains the queue.

## Retention and disk cap

`retention.max_db_bytes` (default 10 GiB) deletes the oldest content, then the oldest spans, when exceeded.
The last hour is never deleted. Details: [retention.md](retention.md).

## Monitoring

| Endpoint | Returns |
|---|---|
| `/healthz` | process up |
| `/readyz` | 200 when ready; 503 otherwise |
| `/metrics` | Prometheus text |
| `/debug/pprof/*` | only with `debug.pprof: true` |

Keep pprof off in production. Never expose the admin port publicly.

| Metric | Labels | Meaning |
|---|---|---|
| `loupe_ingest_spans_total` | `result` (stored, rejected, dropped, duplicate) | Spans by outcome. |
| `loupe_ingest_rejected_total` | `reason` | Rejected spans and logs. |
| `loupe_ingest_dropped_total` | `reason` | Dropped spans and logs. |
| `loupe_ingest_discarded_total` | `signal`, `reason` | Accepted, not stored. |
| `loupe_ingest_queue_spans` | | Queue slots held. |
| `loupe_ingest_queue_bytes` | | Queue bytes. |
| `loupe_ingest_inflight_bytes` | | Request bytes in flight. |
| `loupe_writer_batches_total` | | Committed batches. |
| `loupe_writer_retries_total` | | Write retries. |
| `loupe_writer_jobs_total` | | Writer jobs run. |
| `loupe_writer_job_errors_total` | | Failed writer jobs. |
| `loupe_writer_queued_jobs` | | Jobs waiting. |
| `loupe_writer_batch_duration_seconds` | | Batch commit time (histogram). |
| `loupe_writer_batch_spans` | | Spans per batch (histogram). |
| `loupe_db_size_bytes` | | Main + WAL + SHM size. |
| `loupe_query_duration_seconds` | `endpoint` | Query time (histogram). |
| `loupe_retention_deleted_total` | `class` | Rows deleted by age. |
| `loupe_retention_cap_deleted_total` | `class` | Rows deleted by the cap. |
| `loupe_retention_passes_total` | | Passes completed. |
| `loupe_retention_pass_errors_total` | | Failed passes. |
| `loupe_retention_cap_exceeded_total` | | Passes above the cap. |
| `loupe_retention_cap_unmet_total` | | Passes above the cap with one hour left. |
| `loupe_retention_checkpoint_busy_total` | | Busy checkpoints. |
| `loupe_content_stripped_total` | | Spans and logs with content stripped. |
| `loupe_unpriced_spans_total` | `model` | Spans with no price. |
| `loupe_usage_duplicate_spans_total` | `kind` (llm, embedding) | Duplicate-usage spans. |
| `loupe_build_info` | `version`, `commit`, `go_version` | Build information. |

Useful alerts:

```text
rate(loupe_ingest_rejected_total[5m]) > 0            # overload
increase(loupe_retention_pass_errors_total[1h]) > 0  # retention failing
```

Also alert when `loupe_ingest_dropped_total` or `loupe_retention_cap_unmet_total` increases,
`loupe_ingest_queue_spans` nears `ingest.queue_spans`, `loupe_db_size_bytes` nears
`retention.max_db_bytes`, or `/readyz` fails.

```sh
loupe db stats --json   # sizes, schema version, row counts; safe online
```

## Maintenance

```sh
loupe db vacuum                     # through the running server
loupe db vacuum --offline           # server stopped
loupe db vacuum --offline --full    # full VACUUM; needs free disk of about the database size
loupe pricing recompute --since <date> [--until <date>]   # reprice after editing prices
loupe pricing list                  # effective price table
loupe export [--from] [--to] [--out file]   # JSON Lines; safe online
```

Online commands use `--server` (default from `ui.listen`) and the token from `api.token_file` or
`--token-file`. See [pricing.md](pricing.md).

## Shutdown

On SIGINT or SIGTERM Loupe drains its queue and servers within `shutdown_timeout` (default 30s).
Set your orchestrator grace period above it. A kill -9 loses only the unacknowledged queue.
