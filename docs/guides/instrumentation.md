# Instrumentation

Configure your OpenTelemetry SDK to send LLM traces to Loupe.

Loupe receives OTLP on `:4317` (gRPC) and `:4318` (HTTP). It reads OTel GenAI (`gen_ai.*`),
OpenInference (`llm.*`) and legacy attributes. Each SDK below has a runnable program in `examples/`.

The blocks assume Loupe on `127.0.0.1:4318`. If it listens elsewhere, change
`OTEL_EXPORTER_OTLP_ENDPOINT`. If it needs a receiver token, add
`OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer <token>`.

## Metrics and logs exporters

Leave the SDK defaults on. Loupe accepts OTLP metrics and discards them, so they cause no export errors.
Logs are kept only when they are GenAI log events with a trace and span id. They become content of that span.

## Content opt-in

Prompts and completions need two switches: the SDK must record them, and Loupe needs
`content.capture: on` ([privacy.md](privacy.md)). Each section shows the SDK side.

## Python: OpenTelemetry GenAI (`opentelemetry-instrumentation-openai-v2`)

Example: `examples/python-openai`. Tested with `opentelemetry-instrumentation-openai-v2` 2.4b0 and
`opentelemetry-util-genai` 1.1b0. Version 1.2b0 does not work with 2.4b0.

```sh
OTEL_SERVICE_NAME=my-app                            # service name in Loupe
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
```

Set up the tracer, logger and meter providers and call `OpenAIInstrumentor().instrument()`.
Call `shutdown()` on all three providers before exit, or the last spans are lost.

For content:

```sh
OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental     # required for content
OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=event_only  # or span_only, span_and_event
```

`event_only` sends messages as a log event. `span_only` puts them on the span. Both work.
The value `true` records nothing with this version.

For streaming usage, set `stream_options={"include_usage": True}` on the request.
Without it the span has no tokens.

## Python: OpenInference (`openinference-instrumentation-openai`)

Example: `examples/python-openinference`.

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
OPENINFERENCE_HIDE_INPUTS=false     # true removes prompts at the source
OPENINFERENCE_HIDE_OUTPUTS=false    # true removes completions at the source
```

Pass your tracer provider to `OpenAIInstrumentor().instrument(tracer_provider=...)`.
Content is on by default. Streaming usage also needs `include_usage`.

## Node.js: OpenTelemetry JS (`@opentelemetry/instrumentation-openai`)

Example: `examples/node-openai`. Tested with `openai` 6.49.0 (supported: below 7) and
`@opentelemetry/sdk-node` 0.223.0.

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/json
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true   # content on
```

Use `new NodeSDK({ instrumentations: [new OpenAIInstrumentation()] })`.
Require `openai` after `sdk.start()`. Call `await sdk.shutdown()` before exit.

## Go: manual spans

Example: `examples/go-manual`, using the OTel Go SDK and
`go.opentelemetry.io/contrib/exporters/autoexport`.

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf   # or grpc; http/json is rejected
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
```

On each LLM span (kind `CLIENT`) set `gen_ai.operation.name` (`chat`), `gen_ai.provider.name`,
`gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.usage.input_tokens` and
`gen_ai.usage.output_tokens`. For content, set `gen_ai.input.messages` and `gen_ai.output.messages`.
Shut down the tracer, meter and logger providers before exit.

## Attribute mapping

Loupe reads each field from the first key present, in this order.

| Field | Accepted keys (precedence order) |
|---|---|
| operation | `gen_ai.operation.name` |
| provider | `gen_ai.provider.name`, `gen_ai.system`, `llm.provider`, `llm.system` |
| request model | `gen_ai.request.model`, `llm.request.model`, `llm.request.model_name` |
| response model | `gen_ai.response.model`, `llm.model_name`, `llm.response.model`, `llm.response.model_name` |
| input tokens | `gen_ai.usage.input_tokens`, `gen_ai.usage.prompt_tokens`, `llm.token_count.prompt`, `llm.usage.prompt_tokens` |
| output tokens | `gen_ai.usage.output_tokens`, `gen_ai.usage.completion_tokens`, `llm.token_count.completion`, `llm.usage.completion_tokens` |
| cache read | `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.cache_read_input_tokens`, `llm.token_count.prompt_details.cache_read` |
| cache write | `gen_ai.usage.cache_write.input_tokens`, `gen_ai.usage.cache_creation.input_tokens`, `gen_ai.usage.cache_creation_input_tokens`, `llm.token_count.prompt_details.cache_write` |
| reasoning | `gen_ai.usage.reasoning.output_tokens`, `llm.token_count.completion_details.reasoning` |
| finish reasons | `gen_ai.response.finish_reasons` |
| session | `gen_ai.conversation.id`, `session.id` |
| user | `user.id`, `enduser.id` |
| tool | `gen_ai.tool.name`, `gen_ai.tool.call.id`, `tool.name` |
| agent | `gen_ai.agent.name` |
| service | resource attribute `service.name` |

- The span's model is the response model, else the request model. It is used for pricing.
- Send input and output as totals. Input includes cache tokens; output includes reasoning tokens.
- Total-token keys are kept but never summed or priced.
- Values longer than `receiver.max_attribute_bytes` are truncated and flagged `attr_truncated`.
- Usage is counted once per call. A gateway span and the SDK span below it do not double the bill:
  the repeat is flagged `usage_duplicate` and left out of totals.

## Agents and tools

Example: `examples/agent`. Set `gen_ai.operation.name` to choose the span kind:

| `gen_ai.operation.name` | Kind |
|---|---|
| `chat`, `text_completion`, `generate_content` | `llm` |
| `embeddings` | `embedding` |
| `execute_tool` | `tool` |
| `invoke_agent`, `create_agent` | `agent` |
| `invoke_workflow` | `chain` |
| `retrieval` | `retrieval` |

OpenInference `openinference.span.kind` maps the same way. Parent the `chat` and tool spans
on the agent span, and Loupe shows the tree. Set `gen_ai.conversation.id` (or `session.id`)
to group traces into a session. Tool arguments and results are content.

## Cache and reasoning tokens

Report cache and reasoning tokens with the keys under [Attribute mapping](#attribute-mapping).
Loupe prices them at their own rates when the price file has them.
