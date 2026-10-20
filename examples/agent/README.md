# Python: tool-using agent

A two-step agent against fakellm: an `invoke_agent` span, a `chat` span whose reply is a tool call (fakellm's `tool-call` scenario, selected with the `X-Fake-Scenario` header), an `execute_tool` span, and a final `chat` span. The `chat` spans come from `opentelemetry-instrumentation-openai-v2`, the agent and tool spans are created by hand with the GenAI operation names `invoke_agent` and `execute_tool`; Loupe turns those into the `agent` and `tool` kinds. The session is `gen_ai.conversation.id`.

Pinned SDKs: `tests/python/pyproject.toml` and `uv.lock`.

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
OTEL_SERVICE_NAME=my-agent
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
# content opt-in:
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
answer: lumen juniper ember bravo meadow flint
traces: 1
spans: 4
models: gpt-4o-mini
tokens: input=14 output=13
cost_nanos: 9900
errors: false
invoke_agent weather-agent [agent]
  chat gpt-4o-mini [llm] model=gpt-4o-mini in=6 out=7 cost_nanos=5100
  execute_tool lookup_weather [tool]
  chat gpt-4o-mini [llm] model=gpt-4o-mini in=8 out=6 cost_nanos=4800
```

The token and cost numbers are deterministic: fakellm counts whitespace-separated words and
Loupe prices `gpt-4o-mini` from its built-in price list.

## Content

Chat content arrives as `gen_ai.client.inference.operation.details` log events (see `examples/python-openai`); the tool span carries `gen_ai.tool.call.arguments` and `gen_ai.tool.call.result` attributes, which Loupe treats as content.

Loupe stores prompt and completion content only with `content.capture: on` (set
`LOUPE_CONTENT_CAPTURE=on` on the Loupe container). To see it, run
`LOUPE_CAPTURE=on ./run.sh` against such a Loupe: each span line then ends with
`content=<number of stored content parts>`.

## Other signals

The SDK's default metrics and logs exporters stay on (`OTEL_METRICS_EXPORTER=otlp`,
`OTEL_LOGS_EXPORTER=otlp`). Loupe accepts OTLP metrics and discards them (counted in
`loupe_ingest_discarded_total`), so nothing errors; logs without a `gen_ai.` event name are
discarded the same way.
