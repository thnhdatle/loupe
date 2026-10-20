# Troubleshooting

Each entry gives the symptom, the usual cause and the fix. Error codes are listed
in [errors.md](errors.md), settings in [configuration.md](configuration.md) and
operating details in [operations.md](operations.md). Start with the server log
(`docker compose -f deploy/docker-compose.yml logs loupe`, or
`kubectl logs -n loupe loupe-0`); log lines carry a `component` key and API errors
a `request_id` that matches the response.

## Cannot sign in or call the API

| Symptom | Cause | Fix |
|---|---|---|
| `401 unauthorized` on `/v1/...` | No `Authorization: Bearer <token>` header, or the token is not in `api.token_file`. | Send the token from `api_token`. In the container image, copy it with `docker cp "$(docker compose -f deploy/docker-compose.yml ps -q loupe)":/data/api_token - \| tar -xO` (run on the host: the image has no shell, so `docker exec ... cat` does not work). |
| The login URL says the code is invalid or expired | The `/login?code=...` URL is single use and valid for 15 minutes; every start logs a new one. | Restart Loupe and use the new URL from `grep 'dashboard login'` in the log. Or sign in on `/login` by pasting the API token. |
| No `dashboard login` line in the log | The URL is logged only with `api.generate_token: true` (the image default). | Set `LOUPE_API_GENERATE_TOKEN=true` together with `LOUPE_API_TOKEN_FILE`, or sign in with a token you manage yourself. |
| Login is refused after several attempts | The dashboard allows 5 failed logins per IP per minute. | Wait a minute and retry with the right token. |
| Browser keeps returning to the login page over HTTPS | The session cookie is not marked `Secure` behind a TLS proxy, or the session expired (`ui.session_ttl`, 12h). | Set `ui.secure_cookie: true`; sign in again. |
| `/v1/admin` returns 404 or `loupe db vacuum` cannot reach the server | Without `api.token_file`, the admin API is served only when `ui.listen` is loopback and is disabled otherwise (a warning is logged). | Set `api.token_file`; pass `--token-file` and `--server` to the CLI if the defaults do not match. |

## Startup fails

| Symptom | Cause | Fix |
|---|---|---|
| `listen 127.0.0.1:4318: ... address already in use` | Another process uses the port. | Stop it, or change `receiver.http.listen`, `receiver.grpc.listen`, `ui.listen` or `admin.listen`. With Compose, move the host port with `LOUPE_UI_HOST_PORT`, `LOUPE_GRPC_HOST_PORT`, `LOUPE_HTTP_HOST_PORT`, `LOUPE_ADMIN_HOST_PORT` or `FAKELLM_HOST_PORT` ([quickstart.md](quickstart.md)). |
| Exit code 2 with a list of `  - ...` lines | Configuration validation reports every problem at once, for example two listeners on one port, a non-loopback listener without tokens, a TLS cert without a key. | Fix each line; check with `loupe config validate`. |
| `database is owned by another process` | Another `loupe serve` (or an offline command) holds the lock on `<storage.path>.lock`. One process per database. | Stop the other process. Do not run two replicas on one volume; do not use a network filesystem. |
| `schema version is newer than this binary (downgrades are not supported)` | The database was written by a newer Loupe. | Run the newer version, or stop the server and restore the `<storage.path>.pre-v<N>` file or a backup ([operations.md](operations.md#upgrade)). Data written since the backup is lost. |
| Startup fails during upgrade, free disk mentioned | `storage.backup_before_migrate` needs free space of about the database size. | Free disk space, or move the database to a larger volume. |
| `tokens file <path>: no tokens` | `receiver.tokens_file` or `api.token_file` exists but is empty. | Put one token per line in the file. |
| Non-loopback listener refused at startup | A non-loopback receiver needs `receiver.tokens_file`; a non-loopback `ui.listen` needs `api.token_file`. | Provide tokens, or set the matching `*.allow_unauthenticated: true` only on a trusted network. |
| `loupe healthcheck` fails in a container | It calls the admin `/readyz`; the admin listener is not ready or `admin.listen` is not the default. | Check the log for migration or writer errors; keep `admin.listen` reachable from inside the container. |

## Traces do not appear

| Symptom | Cause | Fix |
|---|---|---|
| Exporter reports connection refused | Loupe listens on loopback by default, or the port is not published. | In Docker publish `-p 127.0.0.1:4318:4318`; in Kubernetes use the Service or `kubectl port-forward`. Use `http/protobuf` on 4318 and gRPC on 4317. |
| Exporter gets 401 or `UNAUTHENTICATED` | The receiver has `receiver.tokens_file`. | Send `Authorization: Bearer <token>` or `X-Loupe-Token`. |
| HTTP 400 or `INVALID_ARGUMENT` | Wrong payload, for example `http/json`, or more than `receiver.max_spans_per_request` spans (`too_many_spans`). | Use `http/protobuf` or gRPC; lower the exporter batch size ([instrumentation.md](instrumentation.md)). |
| HTTP 413 | The request exceeds `receiver.max_request_bytes` (after decompression), or the proxy limit (`client_max_body_size`). | Reduce the exporter batch or raise the limit in both places. |
| Spans arrive but show no model or tokens | The SDK does not emit GenAI attributes Loupe recognises. | See the per-SDK settings in [instrumentation.md](instrumentation.md). |
| Prompts and completions are missing | `content.capture` is `off` by default. | Set `LOUPE_CONTENT_CAPTURE=on` and read [privacy.md](privacy.md). |
| Cost is empty | The model has no price (`loupe_unpriced_spans_total{model}`). | Add it to `pricing.file` and run `loupe pricing recompute --since <date>` ([pricing.md](pricing.md)). |
| Metrics are not stored | OTLP metrics are accepted and discarded by design (`loupe_ingest_discarded_total`). | Use traces; see the [roadmap](../../ROADMAP.md). |

## Overload and rejected data

| Symptom | Cause | Fix |
|---|---|---|
| Exporter logs 429 with `Retry-After: 1`, or gRPC `RESOURCE_EXHAUSTED` | The ingest queue (`ingest.queue_spans`, `ingest.queue_bytes`) or the in-flight byte limit (`receiver.max_inflight_bytes`) is full with `ingest.overflow: reject`. Exporters retry, so nothing is lost while they keep up. | Reduce the send rate or let the exporter back off. The single writer sustains about 15,000 spans/s on the reference machine ([bench/RESULTS.md](../../bench/RESULTS.md)). Raise `writer.batch_spans` for throughput. Check `loupe_ingest_queue_spans` and `loupe_ingest_rejected_total`. |
| Spans disappear, responses are 200 with `partial_success.rejected_spans` | `ingest.overflow: drop`: nothing retries. | Use `reject` unless losing data under load is acceptable. |
| `dropped{reason="store_error"}` and a `batch dropped after retries` error log | A batch failed five times (disk full, I/O error, locked file). | Fix the disk problem; see "Disk is full" below. |
| 503 from `/readyz` or the dashboard | Migrations are pending, the writer is not accepting work, or shutdown has begun. | Read the log; wait for start to finish. During shutdown `/readyz` is 503 by design and the admin API answers `shutting_down`. |
| Spans lost after a crash | With `sync_ack: false` the queue is acknowledged before commit; kill -9 loses what was queued. | Stop gracefully (SIGTERM, grace period above `shutdown_timeout`), or set `ingest.sync_ack: true` at lower throughput. |

## Disk and database size

| Symptom | Cause | Fix |
|---|---|---|
| Old traces vanished early | Retention deleted them (`retention.spans` 14d, `retention.content` 7d) or the disk cap did: `database above retention.max_db_bytes; deleted oldest data` at warn. | Raise the limits and make sure the volume has room: size the disk above the cap (startup warns below twice the cap). See [retention.md](retention.md). |
| Error log `database above retention.max_db_bytes with only the last hour of data left; ingest continues` | The cap is too small for the ingest rate (`loupe_retention_cap_unmet_total` rises). | Raise `retention.max_db_bytes` (minimum 64 MiB when set) or send fewer spans. |
| Disk is full | The cap counts main + WAL + SHM only; backups under `/data/backups`, `.pre-v<N>` files and exports sit outside it. | Delete old backups and `.pre-v<N>` files (Loupe never removes them); keep the volume well above the cap. |
| The file does not shrink after deletes | SQLite reuses freed pages; retention runs an incremental vacuum. | Run `loupe db vacuum`; `loupe db vacuum --offline --full` (server stopped, needs free disk of about the database size) shrinks it fully. |
| `-wal` file is large | A checkpoint was busy (`loupe_retention_checkpoint_busy_total`), usually under a long read. | It is truncated on later passes and at shutdown; see [operations.md](operations.md). |

## Containers and Kubernetes

| Symptom | Cause | Fix |
|---|---|---|
| `docker exec loupe sh` or `... cat` fails with "executable file not found" | The image is distroless: no shell, `cat` or `tar`. | Copy files out from the host: `docker cp loupe:/data/api_token .`. The `loupe` binary itself is runnable: `docker exec loupe /usr/local/bin/loupe db stats`. |
| Compose containers exit or are unhealthy | The data volume is not writable by user 65532, or a host port is taken. | Use a named volume (as `deploy/docker-compose.yml` does) and move ports as above. |
| `kubectl exec ... cat /data/api_token` or `kubectl cp` fails | No `cat` or `tar` in the image. | Use the login URL from `kubectl logs`, or an ephemeral debug container; see [deploy/k8s/README.md](../../deploy/k8s/README.md). |
| Second pod fails to start | Single writer: the file lock allows one process. | Keep `replicas: 1`. |
| Pod killed with OOM | Container memory limit is close to `runtime.memory_limit` (640 MiB soft limit). | Give the container headroom above it, or lower `ingest.queue_bytes` and `receiver.max_inflight_bytes` together with `runtime.memory_limit`. |
| `/metrics` unreachable from Prometheus | `admin.listen` defaults to `127.0.0.1:9092` inside the container. | Set `LOUPE_ADMIN_LISTEN=0.0.0.0:9092` and expose it on a trusted network only (no authentication). |

## Still stuck

Run `loupe config print --redact` and `loupe db stats`, collect the log lines
around the problem, and open an issue with the bug template (version, config,
steps, expected, actual, logs). Report security problems as described in
[SECURITY.md](../../SECURITY.md).
