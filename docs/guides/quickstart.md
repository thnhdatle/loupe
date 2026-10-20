# Quickstart

Run Loupe and an example app, and see its trace, in under five minutes.

## Requirements

Docker with Compose v2, [uv](https://docs.astral.sh/uv/) with Python 3.12, and Go.

## Start Loupe and the example

Run the commands in the [README quickstart](../../README.md#quickstart). They start two containers:

| Container | Listens on (host) |
|---|---|
| `loupe` | dashboard and API `127.0.0.1:3000`, OTLP gRPC `127.0.0.1:4317`, OTLP HTTP `127.0.0.1:4318`, admin `127.0.0.1:9092` |
| `fakellm` | fake OpenAI-compatible provider `127.0.0.1:8090`, no API key needed |

Data is kept in the `loupe-data` volume.

If a port is taken, move it with `LOUPE_UI_HOST_PORT`, `LOUPE_GRPC_HOST_PORT`, `LOUPE_HTTP_HOST_PORT`,
`LOUPE_ADMIN_HOST_PORT` or `FAKELLM_HOST_PORT`. Then point the example at it with
`LOUPE_OTLP_ENDPOINT`, `LOUPE_API` and `FAKELLM_URL`.

## Sign in

```sh
docker compose -f deploy/docker-compose.yml logs loupe | grep 'dashboard login'   # login URL
```

Open the URL within 15 minutes. It works once.

For the REST API, copy the token and send it as `Authorization: Bearer <token>` to `/v1`:

```sh
docker cp "$(docker compose -f deploy/docker-compose.yml ps -q loupe)":/data/api_token - | tar -xO
```

## Send your own traces

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

Any OpenTelemetry SDK works. See [instrumentation.md](instrumentation.md) per SDK.
Prompts and completions are stored only with `LOUPE_CONTENT_CAPTURE=on` ([privacy.md](privacy.md)).

## Stop

```sh
docker compose -f deploy/docker-compose.yml down -v   # -v also deletes your traces
```

Omit `-v` to keep the data.
