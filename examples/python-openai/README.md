# Python: OpenTelemetry GenAI openai-v2

A chat completion against fakellm, traced by `opentelemetry-instrumentation-openai-v2`. Spans, log events and metrics are exported over OTLP/HTTP (protobuf) with the SDK's standard exporters.

Pinned SDKs: `tests/python/pyproject.toml` and `uv.lock` (`uv` and Python 3.12 are needed; `run.sh` runs `uv run --frozen`).

## Setup

Start Loupe and the fake provider (from the repository root):

```sh
docker compose -f deploy/docker-compose.yml up -d --wait
export LOUPE_API_TOKEN="$(docker cp "$(docker compose -f deploy/docker-compose.yml ps -q loupe)":/data/api_token - | tar -xO)"
```

`run.sh` reads these variables; the defaults match the compose file:

| Variable | Default | Meaning |
|---|---|---|
| `LOUPE_OTLP_ENDPOINT` | `http://127.0.0.1:4318` | Loupe's OTLP/HTTP receiver |
| `LOUPE_API` | `http://127.0.0.1:3000` | Loupe's REST API, used to fetch the trace back |
| `LOUPE_API_TOKEN` | empty | Bearer token for the REST API, when one is configured |
| `FAKELLM_URL` | `http://127.0.0.1:8090/v1` | OpenAI-compatible endpoint (fakellm) |

## Environment

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
# content opt-in, log-event form (the default of run.sh):
OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=event_only
```

## Run

```sh
./run.sh
```

`run.sh` runs the app, flushes and shuts down the SDK providers so every span is exported
before exit, then polls Loupe's REST API (up to 10 s) for the trace of this run, found by its
unique `service.name`, and prints:

```text
reply: prairie bravo lumen prairie prairie cedar
traces: 1
spans: 2
models: gpt-4o-mini
tokens: input=4 output=6
cost_nanos: 4200
errors: false
python-openai-example [other]
  chat gpt-4o-mini [llm] model=gpt-4o-mini in=4 out=6 cost_nanos=4200
```

The token and cost numbers are deterministic: fakellm counts whitespace-separated words and
Loupe prices `gpt-4o-mini` from its built-in price list.

## Content

openai-v2 has two content forms; `run.sh` runs either:

- **Log-event form** (default): the env block above. The span carries usage and models; the
  prompt and completion arrive as a `gen_ai.client.inference.operation.details` log event
  that Loupe attaches to the span.
- **Span-attribute form**: `OPENAI_V2_CONTENT_FORM=span ./run.sh`, which sets
  `OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental` and
  `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=span_only`. The content is in the
  `gen_ai.input.messages` and `gen_ai.output.messages` span attributes.

Without the capture variable the instrumentation records no content at all.

Loupe stores prompt and completion content only with `content.capture: on` (set
`LOUPE_CONTENT_CAPTURE=on` on the Loupe container). To see it, run
`LOUPE_CAPTURE=on ./run.sh` against such a Loupe: each span line then ends with
`content=<number of stored content parts>`.

## Other signals

The SDK's default metrics and logs exporters stay on (`OTEL_METRICS_EXPORTER=otlp`,
`OTEL_LOGS_EXPORTER=otlp`). Loupe accepts OTLP metrics and discards them (counted in
`loupe_ingest_discarded_total`), so nothing errors; logs without a `gen_ai.` event name are
discarded the same way.
