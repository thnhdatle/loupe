# Python: app -> Triage -> fakellm

An app calls Triage's OpenAI-compatible endpoint; Triage forwards to fakellm. The app and Triage both export OTLP to Loupe, so one trace holds the app's spans and Triage's.

- The app's `chat`/`embeddings` spans come from `opentelemetry-instrumentation-openai-v2`. A hand-made HTTP client span wraps every request and injects the W3C trace context, so Triage's `route request` span joins the app's trace.
- Triage records a `route request` server span (kind `llm`, or `embedding` on `/v1/embeddings`) with the usage summed over attempts, and one child span per attempt: `chat <model>` or `embeddings <model>`.
- Loupe counts the usage of one call once: `route request` and every attempt are flagged `usage_duplicate` (shown as `duplicate` below), and the call's tokens and cost stay on the app's span. See "Attribute mapping" in [instrumentation.md](../../docs/guides/instrumentation.md#attribute-mapping).

Pinned SDKs: `tests/python/pyproject.toml` and `uv.lock`. Triage runs from its image (`ghcr.io/thnhdatle/triage:v0.1.0`, or `RAYLD_TRIAGE_IMAGE`), configured by `triage.yaml`: a chat route `triage-auto` (weak target `fake-small`, fallback to the strong target `fake-strong`) and an embeddings route `triage-embed` (target `fake-embed`), all on fakellm, privacy off.

## Setup

Start Loupe and the fake provider (from the repository root):

```sh
docker compose -f deploy/docker-compose.yml up -d --wait
export LOUPE_API_TOKEN="$(docker cp "$(docker compose -f deploy/docker-compose.yml ps -q loupe)":/data/api_token - | tar -xO)"
```

`run.sh` starts and stops the Triage container itself. It reads these variables; the defaults match the compose file:

| Variable | Default | Meaning |
|---|---|---|
| `LOUPE_OTLP_ENDPOINT` | `http://127.0.0.1:4318` | Loupe's OTLP/HTTP receiver |
| `LOUPE_API` | `http://127.0.0.1:3000` | Loupe's REST API, used to fetch the traces back |
| `LOUPE_API_TOKEN` | empty | Bearer token for the REST API, when one is configured |
| `FAKELLM_URL` | `http://127.0.0.1:8090/v1` | fakellm; also its `/_fake/script` control API |
| `TRIAGE_PORT` | `8080` | host port of the Triage container |
| `RAYLD_TRIAGE_IMAGE` | `ghcr.io/thnhdatle/triage:v0.1.0` | Triage image |

Loopback addresses of the Loupe and fakellm URLs are rewritten to `host.docker.internal` for the Triage container.

## Environment

The app:

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

Triage (tracing is off unless an endpoint is set; `tracing.capture_content: true` in its config adds prompt and response text to the attempt spans, which Loupe stores only with `content.capture: on`):

```sh
OTEL_SERVICE_NAME=triage
OTEL_EXPORTER_OTLP_ENDPOINT=http://host.docker.internal:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

## Run

```sh
./run.sh
```

The app runs four calls, each its own trace (fakellm's per-model script, `PUT /_fake/script`, makes the second and third fail over):

1. one chat attempt;
2. the weak target answers 500 (an attempt without usage), the strong target answers;
3. a streamed answer cut after three chunks on the weak target, continued by the strong target (the cut attempt reports no usage, the continuation reports the totals);
4. an embeddings call.

`run.sh` flushes the SDK providers, stops Triage (which flushes its spans), then polls Loupe's REST API (up to 10 s) for the traces of this run, found by the unique `service.name`, and prints:

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

Tokens are counted once per call. Cost is 0 here because Loupe's built-in price list has no `fake-*` models; with a price for them in `pricing.file` the cost sits on the app's span (an unpriced caller takes the cost of its duplicates).

## Recorded payloads

`scripts/record_triage.sh` records these four traces into `testdata/contracts/triage/`; they feed the usage-dedupe golden test (`internal/adapter/sqlite/triage_golden_test.go`).
