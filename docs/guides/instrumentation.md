# Instrumentation

Loupe receives OTLP on `:4317` (gRPC) and `:4318` (HTTP: protobuf or JSON, optionally gzip).
It understands three attribute families, with this precedence when a span mixes them: OTel
GenAI (`gen_ai.*`), OpenInference (`llm.*`, `openinference.span.kind`) and the older
legacy names. Any SDK that emits one of them works; this guide gives the exact
configuration for the SDKs the repository tests against. Each has a runnable program in
`examples/`, checked by `make examples`, and the versions are pinned in `tests/python/uv.lock`
and `tests/node/package-lock.json`.

Every block below assumes Loupe on `127.0.0.1:4318`. If Loupe listens elsewhere or requires
a receiver token, change `OTEL_EXPORTER_OTLP_ENDPOINT` and add
`OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer <token>`.

## Metrics and logs exporters

Leave the SDK defaults on. Loupe accepts OTLP **metrics** and discards them (counted in
`loupe_ingest_discarded_total{signal="metrics"}`), so a default `OTEL_METRICS_EXPORTER=otlp`
never produces export errors. OTLP **logs** are kept only when they are GenAI log events
(event name starting with `gen_ai.`, with a trace and span id); they become content of the
span. Every other log record is discarded and counted the same way. The event name is read
from the LogRecord `EventName` field, else the `event.name` attribute, else
`otel.event.name`.

## Content opt-in

Most GenAI instrumentations record no prompts or completions unless you ask. Loupe also
stores nothing unless `content.capture` is `on` (see `privacy.md`): both switches are needed.
Each section lists the SDK side.

## Python: OpenTelemetry GenAI (`opentelemetry-instrumentation-openai-v2`)

Example: `examples/python-openai`. Tested with `opentelemetry-instrumentation-openai-v2`
2.4b0 and `opentelemetry-util-genai` 1.1b0 (1.2b0 is not compatible with openai-v2 2.4b0).

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
```

In code, set up the tracer, logger and meter providers with the OTLP/HTTP exporters and call
`OpenAIInstrumentor().instrument()`; call `shutdown()` on all three providers before the
process exits or the last spans are lost.

Content has two forms. Both need the latest conventions switch:

```sh
OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
```

- **Log-event form**: `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=event_only`. The
  `chat` span has usage, models and finish reasons; the messages arrive as a
  `gen_ai.client.inference.operation.details` log event (body empty, messages in the
  `gen_ai.input.messages` and `gen_ai.output.messages` attributes) on the span's trace and
  span id. Loupe stores it as content of that span even if the log reaches Loupe first.
- **Span-attribute form**: `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=span_only`. The
  messages are the `gen_ai.input.messages` and `gen_ai.output.messages` span attributes.

`span_and_event` emits both. The legacy value `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true`
records nothing with this version; it is rejected by `opentelemetry-util-genai`.

Streaming calls report usage only when the request sets `stream_options={"include_usage": True}`;
without it the span has no `gen_ai.usage.*` and Loupe records the span with no usage.

## Python: OpenInference (`openinference-instrumentation-openai`)

Example: `examples/python-openinference`.

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
OPENINFERENCE_HIDE_INPUTS=false
OPENINFERENCE_HIDE_OUTPUTS=false
```

Pass your tracer provider to `OpenAIInstrumentor().instrument(tracer_provider=...)`. Spans have
kind `ChatCompletion` with `openinference.span.kind=LLM`, `llm.model_name`,
`llm.token_count.*` and content in `input.value`, `output.value`, `llm.input_messages.*` and
`llm.output_messages.*`: content is on by default and the two `HIDE` variables remove it at
the source. OpenInference emits no logs. Streaming usage again needs `include_usage`.

## Node.js: OpenTelemetry JS (`@opentelemetry/instrumentation-openai`)

Example: `examples/node-openai`. Tested with `openai` 6.49.0 (the instrumentation supports
`openai` below 7) and `@opentelemetry/sdk-node` 0.223.0.

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/json
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true
```

`new NodeSDK({ instrumentations: [new OpenAIInstrumentation()] })` builds the three exporters
from this environment; require `openai` after `sdk.start()` and call `await sdk.shutdown()` before
exit. Spans use `gen_ai.system` (not `gen_ai.provider.name`), `server.address` and
`gen_ai.usage.input_tokens`/`output_tokens`. With content on, each call also sends a
`gen_ai.user.message` and a `gen_ai.choice` log event whose name is in the `event.name`
attribute.

## Go: manual spans

Example: `examples/go-manual`, its own module using the OTel Go SDK and
`go.opentelemetry.io/contrib/exporters/autoexport`.

```sh
OTEL_SERVICE_NAME=my-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_TRACES_EXPORTER=otlp
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
```

The Go exporter speaks `http/protobuf` or `grpc`; `http/json` is rejected. Set on each LLM
span, with kind `CLIENT`: `gen_ai.operation.name` (`chat`), `gen_ai.provider.name`,
`gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.usage.input_tokens` and
`gen_ai.usage.output_tokens`. Content, if you want it, is your choice of attribute:
`gen_ai.input.messages` and `gen_ai.output.messages`. Shut down the tracer, meter and logger
providers before exit.

## Attribute mapping

Loupe maps each span onto a fixed set of fields. Each field accepts the keys below in
precedence order; the first key with a usable value wins (integers may also be sent as an
integral double or a numeric string; negative or fractional values are skipped).

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

- The span's model is the response model, else the request model; it is the model shown in
  the dashboard and the one used for pricing.
- Token counts are treated as inclusive in every family: input includes cache-read and
  cache-write tokens, output includes reasoning tokens.
- Total-token keys (`gen_ai.usage.total_tokens`, `llm.token_count.total`,
  `llm.usage.total_tokens`) are kept in the attributes as a cross-check and are never summed
  or priced. Modality breakdowns (`gen_ai.usage.{text,image,audio}.*`,
  `llm.token_count.prompt_details.{audio,cache_input}`,
  `llm.token_count.completion_details.audio`) are kept but not mapped.
- Every non-content attribute is stored in the span's `attributes` JSON (mapped keys
  included); content keys never are (see `privacy.md`). String values longer than
  `receiver.max_attribute_bytes` are truncated and the span is flagged `attr_truncated`.
- Usage is counted once per call: an `llm` or `embedding` span whose tokens equal those of
  its nearest ancestor of the same kind, or whose sibling group sums to that ancestor's, is
  flagged `usage_duplicate` and left out of token and cost totals. A gateway span and the
  SDK span below it therefore do not double the bill.

## Agents and tools

Example: `examples/agent`. The kind of a span comes from `gen_ai.operation.name`: `chat`,
`text_completion` and `generate_content` are `llm`; `embeddings` is `embedding`;
`execute_tool` is `tool`; `invoke_agent` and `create_agent` are `agent`; `invoke_workflow` is
`chain`; `retrieval` is `retrieval`. OpenInference `openinference.span.kind` values map the
same way (`LLM`, `EMBEDDING`, `TOOL`, `RETRIEVER` to `retrieval`, `RERANKER` to `rerank`,
`AGENT`, `CHAIN`, `GUARDRAIL`, case-insensitive). `gen_ai.operation.name` wins over
`openinference.span.kind` and over the span name or OTLP SpanKind; a span with neither is
`llm` if it has a model and usage, else `other`. Parent the `chat` spans and the tool spans on the agent span (a normal OTel context)
and Loupe shows the tree. Set `gen_ai.conversation.id` (or `session.id`) to group traces
into a session. Tool arguments and results (`gen_ai.tool.call.arguments`,
`gen_ai.tool.call.result`) are content.

## Cache and reasoning tokens

Providers that bill cache and reasoning tokens separately report them with the keys in the
table under [Attribute mapping](#attribute-mapping) (for example `gen_ai.usage.cache_read.input_tokens`,
`gen_ai.usage.cache_creation.input_tokens`, `gen_ai.usage.reasoning.output_tokens`,
`llm.token_count.prompt_details.cache_read`). Report input and output tokens as totals that
include them; Loupe prices the cache and reasoning parts at their own rates when the price
file has them. fakellm emits none of these keys, so the `handwritten-*` payloads in
`testdata/otlp/` cover them.

## Recording payloads

`scripts/record_otlp.sh` runs every example against a tiny capture server
(`bench/cmd/otlpcapture`) instead of Loupe and writes the raw requests as OTLP/JSON to
`testdata/otlp/<family>/recorded-*.json`; the golden tests in `internal/adapter/otlp` translate
them and compare with `*.golden.json` (`go test ./internal/adapter/otlp -run Golden -update`
rewrites them).
