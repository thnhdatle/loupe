# Python: tool-using agent

Trace a two-step agent against fakellm: `invoke_agent`, `chat` (replies with a tool call), `execute_tool`, `chat`.
Loupe shows them as the `agent`, `llm` and `tool` kinds. The session is `gen_ai.conversation.id`.

Pinned SDKs: `tests/python/pyproject.toml` and `uv.lock`.

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

It waits up to 10 s for the trace, then prints:

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

The numbers are deterministic: fakellm counts words, Loupe uses its built-in prices.

## Content

Chat content arrives as `gen_ai.client.inference.operation.details` log events (see `examples/python-openai`).
The tool span carries `gen_ai.tool.call.arguments` and `gen_ai.tool.call.result`, which Loupe treats as content.

To see stored content, set `LOUPE_CONTENT_CAPTURE=on` on the Loupe container, then:

```sh
LOUPE_CAPTURE=on ./run.sh   # each span line ends with content=<stored content parts>
```

## Other signals

Loupe discards metrics and non-gen_ai logs without error (counted in `loupe_ingest_discarded_total`).
