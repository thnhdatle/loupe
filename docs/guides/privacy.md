# Privacy

Control which prompts and completions Loupe stores, mask them, and delete a user's data.

Loupe stores nothing of the content unless you turn it on. It sends no telemetry and makes no
external requests unless you configure [Analyze](#analyze). It never writes content to its logs.

## Content capture modes

```yaml
content:
  capture: on   # off (default), metadata_only or on
```

| Mode | Stored | Notes |
|---|---|---|
| `off` (default) | Only a marker and the content size. | Dashboard shows "content not stored: set content.capture=on" |
| `metadata_only` | Role and size of each part. | No text |
| `on` | Redacted, truncated text. | |

Stored content is kept for `retention.content` (default 7d). The disk cap deletes content first.

The SDK must also send content. Most SDKs need an opt-in ([instrumentation.md](instrumentation.md)).

## What counts as content

These carriers are content. They are removed from stored attributes in every mode.

- span attributes: `gen_ai.input.messages`, `gen_ai.output.messages`,
  `gen_ai.system_instructions`, `gen_ai.tool.call.arguments`,
  `gen_ai.tool.call.result`, `gen_ai.prompt`, `gen_ai.completion`,
  `gen_ai.prompt.*`, `gen_ai.completion.*`, `input.value`, `output.value`,
  `llm.input_messages.*`, `llm.output_messages.*`, `llm.prompt_template.*`,
  `llm.prompts.*`, `tool_call.function.arguments`, `tool.parameters`,
  `retrieval.documents.*`, `reranker.*.documents.*`, `reranker.*_documents.*`,
  `embedding.embeddings.*.text`;
- span events named `gen_ai.*`;
- GenAI log events: `gen_ai.system.message`, `gen_ai.user.message`,
  `gen_ai.assistant.message`, `gen_ai.tool.message`, `gen_ai.choice` and
  `gen_ai.client.inference.operation.details`.

Content whose span never arrives is deleted after `retention.orphan_content` (default 1h).

## Truncation

In `on` mode, `content.max_bytes` (default 32 KiB) limits the stored text per span, after redaction.
Cut parts are flagged truncated.

## Redaction

```yaml
content:
  capture: on
  redact:
    - email                       # preset
    - api_key                     # preset
    - { name: ticket, pattern: "TCK-[0-9]+", replacement: "[TICKET]" }   # custom rule
```

The default is `[email, api_key]`. An empty list turns redaction off.

Redaction covers stored content and the string values of other span attributes.
It does not cover span names, resource attributes or other typed fields. Do not put secrets there.

### Presets

| Preset | Replacement | What it matches |
|---|---|---|
| `email` | `[EMAIL]` | `[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}` |
| `phone` | `[PHONE]` | `\+\d{7,15}\|(?:\+\d{1,3}[ .\-]?)?(?:\(\d{1,4}\)[ .\-]?)?\d{2,4}(?:[ .\-]\d{2,4}){1,4}`, with 7-15 digits; not UUIDs, ISO dates or IPv4 addresses |
| `credit_card` | `[CREDIT_CARD]` | `\d(?:[ \-]?\d){12,18}`, passing the Luhn check |
| `api_key` | `[API_KEY]` | `\bsk-(?:proj-\|ant-)?[A-Za-z0-9_\-]{20,}`, `\bgh[pos]_[A-Za-z0-9]{20,}`, `\bgithub_pat_[A-Za-z0-9_]{20,}`, `\bAKIA[0-9A-Z]{16}\b`, `\bxox[abprs]-[A-Za-z0-9\-]{10,}`, `\bAIza[0-9A-Za-z_\-]{35}`; also `Bearer <token>` (20+ characters), keeping `Bearer` |

Presets are heuristics. They miss unknown formats and can match look-alikes. They are a safety net, not a guarantee.

### Custom rules

`{name, pattern, replacement}`: `name` is required and unique, `pattern` is a Go (RE2) regex,
`replacement` may use `$1`. Invalid rules fail `loupe config validate` and startup with exit code 2.

Test patterns on real samples. Too narrow leaks data. Too broad destroys data you wanted.

## Custom application keys are not stripped

Only the listed keys are content. Your own keys (`app.prompt`, `customer.email`) are stored in every mode,
shown in the API and dashboard, and exported. Mask their values with redact rules, or do not send them.
The same holds for span names, resource attributes, `user.id` and `session.id`.

## Deleting a user's data

```text
DELETE /v1/users/{user_id}/data   # needs the API token; returns 202 and a job_id
GET /v1/admin/jobs/{id}           # job status
```

Ingest continues while it runs.

It deletes every trace that involves the user (whole, even if shared with other users), the user's
sessions, and the user's per-user rollups. Aggregate rollups without a user dimension are kept.

It does **not** delete:

- backups in `backups/` and files from `loupe db backup`;
- `<storage.path>.pre-v<N>` copies made before migrations;
- your own copies: volume snapshots, exports.

Handle those by your own retention rules. New traces for the user can still arrive; stop them upstream.

## Analyze

The Analyze button on a trace page (or `POST /v1/traces/{trace_id}/analyze`) explains a failing or slow
trace. Loupe lists findings from the stored spans (errors, rate limits, retries, slow spans, large
prompts, cost share), then sends them with a span table (name, kind, model, status, timing, tokens,
cost, error text) to the model at `analyze.base_url` and shows its causes and fixes, linked to spans.
Nothing is sent until someone clicks, and Analyze is off while `analyze.base_url` is empty.

Prompt and completion text is sent only with `content.capture: on` and `analyze.include_content: true`,
as stored (already redacted). Use a local model, or point `analyze.base_url` at Triage to mask personal
data first ([configuration.md](configuration.md#analyze)).

## Where the data lives

One SQLite file, `storage.path`, plus its `-wal` and `-shm` files and the backups above.
Use disk encryption if the host is shared. Put TLS in front of any non-loopback listener ([operations.md](operations.md)).
