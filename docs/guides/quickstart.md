# Quickstart

Goal: Loupe running, an instrumented example app sending traces, and the trace visible, in under five minutes.

## Requirements

Docker with Compose v2, [uv](https://docs.astral.sh/uv/) with Python 3.12 (for the Python example) and Go (the example prints its summary with a small Go helper).

## Start Loupe and the example

The commands are in the [README quickstart](../../README.md#quickstart). They start two containers from `deploy/docker-compose.yml`:

- `loupe`: the image defaults. Data is in the `loupe-data` volume at `/data`; the dashboard and REST API listen on host `127.0.0.1:3000`, OTLP gRPC on `127.0.0.1:4317`, OTLP HTTP on `127.0.0.1:4318`, the admin endpoints on `127.0.0.1:9092`.
- `fakellm`: an OpenAI-compatible fake provider on `127.0.0.1:8090`, so the example needs no API key.

If a host port is taken, move it with `LOUPE_UI_HOST_PORT`, `LOUPE_GRPC_HOST_PORT`, `LOUPE_HTTP_HOST_PORT`, `LOUPE_ADMIN_HOST_PORT` or `FAKELLM_HOST_PORT`, and tell the example with `LOUPE_OTLP_ENDPOINT`, `LOUPE_API` and `FAKELLM_URL`.

## Sign in

The image generates an API token on first start (`/data/api_token`) and logs a single-use dashboard login URL on every start:

```sh
docker compose -f deploy/docker-compose.yml logs loupe | grep 'dashboard login'
```

Open the URL within 15 minutes. It sets a session cookie and redirects to the dashboard. The token itself is never logged; copy it with
`docker cp "$(docker compose -f deploy/docker-compose.yml ps -q loupe)":/data/api_token - | tar -xO` and send it as `Authorization: Bearer <token>` to `/v1`.

## Send your own traces

Any OpenTelemetry SDK works:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

See [instrumentation.md](instrumentation.md) for per-SDK settings and [configuration.md](configuration.md) for options. Prompt and completion content is stored only with `LOUPE_CONTENT_CAPTURE=on` ([privacy.md](privacy.md)).

## Stop

```sh
docker compose -f deploy/docker-compose.yml down -v
```

`-v` also deletes the data volume; omit it to keep your traces.
