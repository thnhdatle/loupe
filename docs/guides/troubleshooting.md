# Troubleshooting

Find the fix for a Loupe problem from its symptom.

Start with the server log:

```sh
docker compose -f deploy/docker-compose.yml logs loupe   # Compose
kubectl logs -n loupe loupe-0                            # Kubernetes
```

API errors carry a `request_id` that matches the log. Codes: [errors.md](errors.md).
Settings: [configuration.md](configuration.md).

## Cannot sign in or call the API

| Symptom | Cause | Fix |
|---|---|---|
| `401 unauthorized` on `/v1/...` | Missing or unknown bearer token. | Send the token from `api_token`. In the image: `docker cp "$(docker compose -f deploy/docker-compose.yml ps -q loupe)":/data/api_token - \| tar -xO` on the host. |
| The login URL says the code is invalid or expired | It works once, for 15 minutes. | Restart and use the new URL (`grep 'dashboard login'`), or paste the API token on `/login`. |
| No `dashboard login` line in the log | Needs `api.generate_token: true`. | Set `LOUPE_API_GENERATE_TOKEN=true` with `LOUPE_API_TOKEN_FILE`, or use your own token. |
| Login is refused after several attempts | 5 failed logins per IP per minute. | Wait a minute. |
| Browser keeps returning to the login page over HTTPS | Cookie not `Secure` behind a TLS proxy, or session expired (`ui.session_ttl`, 12h). | Set `ui.secure_cookie: true`; sign in again. |
| `/v1/admin` returns 404 or `loupe db vacuum` cannot reach the server | No `api.token_file` and non-loopback `ui.listen`. | Set `api.token_file`; pass `--token-file` and `--server`. |

## Startup fails

| Symptom | Cause | Fix |
|---|---|---|
| `listen 127.0.0.1:4318: ... address already in use` | Port taken. | Stop the other process or change the listen address. With Compose, use the `*_HOST_PORT` variables ([quickstart.md](quickstart.md)). |
| Exit code 2 with a list of `  - ...` lines | Invalid configuration. | Fix each line; check with `loupe config validate`. |
| `database is owned by another process` | Another Loupe process holds the database. | Stop it. One process per database; no network filesystem. |
| `schema version is newer than this binary (downgrades are not supported)` | Written by a newer Loupe. | Run the newer version, or restore the `<storage.path>.pre-v<N>` file or a backup ([operations.md](operations.md#upgrade)). Data written since is lost. |
| Startup fails during upgrade, free disk mentioned | The pre-migration backup needs about the database size. | Free disk, or move to a larger volume. |
| `tokens file <path>: no tokens` | Token file is empty. | One token per line. |
| Non-loopback listener refused at startup | No tokens configured. | Set `receiver.tokens_file` or `api.token_file`, or `*.allow_unauthenticated: true` on a trusted network only. |
| `loupe healthcheck` fails in a container | Admin `/readyz` not ready or not reachable. | Check the log for errors; keep `admin.listen` reachable inside the container. |

## Traces do not appear

| Symptom | Cause | Fix |
|---|---|---|
| Exporter reports connection refused | Loopback listener, or port not published. | Docker: `-p 127.0.0.1:4318:4318`. Kubernetes: the Service or `kubectl port-forward`. `http/protobuf` on 4318, gRPC on 4317. |
| Exporter gets 401 or `UNAUTHENTICATED` | Receiver tokens are set. | Send `Authorization: Bearer <token>` or `X-Loupe-Token`. |
| HTTP 400 or `INVALID_ARGUMENT` | Bad payload, or over `receiver.max_spans_per_request` (`too_many_spans`). | Use `http/protobuf` or gRPC; lower the exporter batch size. |
| HTTP 413 | Over `receiver.max_request_bytes` or the proxy's `client_max_body_size`. | Lower the batch or raise both limits. |
| Spans arrive but show no model or tokens | No recognised GenAI attributes. | See [instrumentation.md](instrumentation.md). |
| Prompts and completions are missing | `content.capture` is `off` by default. | Set `LOUPE_CONTENT_CAPTURE=on`; read [privacy.md](privacy.md). |
| Cost is empty | No price for the model. | Add it to `pricing.file`; run `loupe pricing recompute --since <date>` ([pricing.md](pricing.md)). |
| Metrics are not stored | OTLP metrics are discarded by design. | Use traces; see the [roadmap](../../ROADMAP.md). |

## Overload and rejected data

| Symptom | Cause | Fix |
|---|---|---|
| Exporter logs 429 with `Retry-After: 1`, or gRPC `RESOURCE_EXHAUSTED` | Queue or in-flight limit full. Exporters retry. | Send slower or let the exporter back off. One writer handles about 15,000 spans/s ([bench/RESULTS.md](../../bench/RESULTS.md)). Raise `writer.batch_spans`. |
| Spans disappear, responses are 200 with `partial_success.rejected_spans` | `ingest.overflow: drop`. | Use `reject` unless data loss is acceptable. |
| `dropped{reason="store_error"}` and a `batch dropped after retries` error log | Writes failed five times (disk full, I/O). | Fix the disk; see "Disk is full". |
| 503 from `/readyz` or the dashboard | Migrations pending, writer not accepting work, or shutting down. | Read the log; wait for start. |
| Spans lost after a crash | Queued spans are acknowledged before commit. | Stop with SIGTERM (grace period above `shutdown_timeout`), or set `ingest.sync_ack: true`. |

## Disk and database size

| Symptom | Cause | Fix |
|---|---|---|
| Old traces vanished early | Retention or the disk cap (`database above retention.max_db_bytes; deleted oldest data`). | Raise the limits; size the disk above the cap (startup warns below twice the cap). See [retention.md](retention.md). |
| Error log `database above retention.max_db_bytes with only the last hour of data left; ingest continues` | Cap too small for the ingest rate. | Raise `retention.max_db_bytes` (minimum 64 MiB) or send less. |
| Disk is full | Backups, `.pre-v<N>` files and exports are outside the cap. | Delete old backups and `.pre-v<N>` files; Loupe never removes them. |
| The file does not shrink after deletes | SQLite reuses freed pages. | `loupe db vacuum`, or `loupe db vacuum --offline --full` (server stopped, free disk about the database size). |
| `-wal` file is large | A busy checkpoint, usually a long read. | It shrinks on later passes and at shutdown. |

## Containers and Kubernetes

| Symptom | Cause | Fix |
|---|---|---|
| `docker exec loupe sh` or `... cat` fails with "executable file not found" | No shell, `cat` or `tar` in the image. | From the host: `docker cp loupe:/data/api_token .`. Or run `docker exec loupe /usr/local/bin/loupe db stats`. |
| Compose containers exit or are unhealthy | Volume not writable by user 65532, or port taken. | Use a named volume; move ports. |
| `kubectl exec ... cat /data/api_token` or `kubectl cp` fails | No `cat` or `tar` in the image. | Use the login URL from `kubectl logs`, or a debug container ([deploy/k8s/README.md](../../deploy/k8s/README.md)). |
| Second pod fails to start | One process per database. | Keep `replicas: 1`. |
| Pod killed with OOM | Memory limit too close to `runtime.memory_limit` (640 MiB). | Add headroom, or lower `ingest.queue_bytes`, `receiver.max_inflight_bytes` and `runtime.memory_limit` together. |
| `/metrics` unreachable from Prometheus | `admin.listen` is `127.0.0.1:9092` in the container. | Set `LOUPE_ADMIN_LISTEN=0.0.0.0:9092` on a trusted network only (no authentication). |

## Still stuck

```sh
loupe config print --redact   # your effective config
loupe db stats                # database state
```

Open an issue with the bug template, including this output and the log lines.
Report security problems as described in [SECURITY.md](../../SECURITY.md).
