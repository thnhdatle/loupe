# Python: app -> Triage -> fakellm

Trace an app that calls Triage, which forwards to fakellm. App and Triage spans land in one trace.

- App spans: `opentelemetry-instrumentation-openai-v2`, plus an HTTP client span that injects W3C trace context.
- Triage spans: `route request` (kind `llm`, or `embedding` on `/v1/embeddings`) and one child per attempt.
- Loupe counts usage once. `route request` and attempts are flagged `usage_duplicate` (shown as `duplicate`).
  See [attribute mapping](../../docs/guides/instrumentation.md#attribute-mapping).

Pinned SDKs: `tests/python/pyproject.toml` and `uv.lock`. Triage runs from `ghcr.io/thnhdatle/triage:v0.1.0`
(or `RAYLD_TRIAGE_IMAGE`) with `triage.yaml`:

| Route | Targets |
|---|---|
| `triage-auto` (chat) | weak `fake-small`, fallback to strong `fake-strong` |
| `triage-embed` (embeddings) | `fake-embed` |

All targets are on fakellm. Privacy is off.

## Setup

From the repository root:

```sh
docker compose -f deploy/docker-compose.yml up -d --wait   # Loupe and fakellm
export LOUPE_API_TOKEN="$(docker cp "$(docker compose -f deploy/docker-compose.yml ps -q loupe)":/data/api_token - | tar -xO)"
```

`run.sh` starts and stops the Triage container. Defaults match the compose file.

| Variable | Default | Meaning |
|---|---|---|
| `LOUPE_OTLP_ENDPOINT` | `http://127.0.0.1:4318` | OTLP/HTTP receiver |
| `LOUPE_API` | `http://127.0.0.1:3000` | REST API, to fetch the traces back |
| `LOUPE_API_TOKEN` | empty | REST API bearer token, if configured |
| `FAKELLM_URL` | `http://127.0.0.1:8090/v1` | fakellm, also its `/_fake/script` API |
| `TRIAGE_PORT` | `8080` | Triage host port |
| `RAYLD_TRIAGE_IMAGE` | `ghcr.io/thnhdatle/triage:v0.1.0` | Triage image |

Loopback Loupe and fakellm URLs become `host.docker.internal` inside the Triage container.

## Environment

The app (`run.sh` sets the same, with a unique service name):

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
# content opt-in:
OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=event_only
```

Triage (tracing is off unless an endpoint is set):

```sh
OTEL_SERVICE_NAME=triage
OTEL_EXPORTER_OTLP_ENDPOINT=http://host.docker.internal:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

`tracing.capture_content: true` in the Triage config adds prompt and response text to attempt spans.
Loupe stores it only with `content.capture: on`.

## Run

```sh
./run.sh
```

The app makes four calls, one trace each:

1. one chat attempt;
2. the weak target answers 500 (no usage), the strong target answers;
3. a stream cut after three chunks on the weak target (no usage), continued by the strong target (totals);
4. an embeddings call.

`run.sh` stops Triage to flush its spans, then polls the REST API (up to 10 s). It prints:

```text
a: iris prairie juniper nectar lumen kestrel
b1: onyx lumen kestrel onyx iris prairie
b2 words: 44
c dims: 1024
traces: 4
== trace 1
spans: 4
models: fake-small
tokens: input=2 output=6
cost_nanos: 0
errors: false
chat triage-auto [llm] model=fake-small in=2 out=6
  POST [other]
    route request [llm] model=fake-small in=2 out=6 duplicate
      chat fake-small [llm] model=fake-small in=2 out=6 duplicate
== trace 2
...
```

Tokens are counted once per call. Cost is 0 because the built-in price list has no `fake-*` models.
With a price in `pricing.file`, cost sits on the app's span. An unpriced caller takes the cost
of its duplicates.

## Recorded payloads

```sh
scripts/record_triage.sh   # records these four traces into testdata/contracts/triage/
```

They feed the golden test `internal/adapter/sqlite/triage_golden_test.go`.
