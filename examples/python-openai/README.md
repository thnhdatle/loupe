# Python: OpenTelemetry GenAI openai-v2

Trace a chat completion against fakellm with `opentelemetry-instrumentation-openai-v2`, exported over OTLP/HTTP (protobuf).

Pinned SDKs: `tests/python/pyproject.toml` and `uv.lock`. Needs `uv` and Python 3.12 (`run.sh` runs `uv run --frozen`).

## Setup

From the repository root:

```sh
docker compose -f deploy/docker-compose.yml up -d --wait   # Loupe and fakellm
export LOUPE_API_TOKEN="$(docker cp "$(docker compose -f deploy/docker-compose.yml ps -q loupe)":/data/api_token - | tar -xO)"
```

Defaults match the compose file.

| Variable | Default | Meaning |
|---|---|---|
| `LOUPE_OTLP_ENDPOINT` | `http://127.0.0.1:4318` | OTLP/HTTP receiver |
| `LOUPE_API` | `http://127.0.0.1:3000` | REST API, to fetch the trace back |
| `LOUPE_API_TOKEN` | empty | REST API bearer token, if configured |
| `FAKELLM_URL` | `http://127.0.0.1:8090/v1` | fakellm (OpenAI-compatible) |

## Environment

Use these in your own app. `run.sh` sets the same, with a unique service name.

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

It waits up to 10 s for the trace, then prints:

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

The numbers are deterministic: fakellm counts words, Loupe uses its built-in prices.

## Content

```sh
./run.sh                              # log-event form: a gen_ai.client.inference.operation.details event
OPENAI_V2_CONTENT_FORM=span ./run.sh  # span-attribute form: gen_ai.input.messages, gen_ai.output.messages
```

The span form sets `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=span_only`. Without the capture
variable, no content is recorded.

To see stored content, set `LOUPE_CONTENT_CAPTURE=on` on the Loupe container, then:

```sh
LOUPE_CAPTURE=on ./run.sh   # each span line ends with content=<stored content parts>
```

## Other signals

Loupe discards metrics and non-gen_ai logs without error (counted in `loupe_ingest_discarded_total`).
