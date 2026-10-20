# Privacy

Loupe stores traces from your own applications on your own disk. Prompts and
completions are the sensitive part, so Loupe does not store them unless you ask.
This guide covers what is captured, how to mask it, how to delete a user's data,
and what Loupe does not protect.

Nothing leaves the machine. Loupe sends no telemetry, makes no external requests
(the dashboard works offline) and does not export its own traces: in v0.1
`self_tracing.endpoint` is validated but ignored. Loupe never writes
content to its logs at any log level.

## Content capture modes

`content.capture` applies to every carrier of content (see the denylist below),
in all three attribute families.

| Mode | Stored | Notes |
|---|---|---|
| `off` (default) | Nothing from the content. One content marker per span or log event records that content arrived and its total size in bytes; the body is NULL. | The dashboard shows "content not stored: set content.capture=on" |
| `metadata_only` | One row per content part with role and `bytes`; body NULL. | Roles and lengths, no text |
| `on` | Parts in the `span_content` table, zstd-compressed, redacted and truncated. | Role and index come from the key or event name when present |

`loupe_content_stripped_total` counts spans and log records whose content was
stripped (`off` and `metadata_only`). Retention of stored content is
`retention.content` (default 7d, never more than `retention.spans`); content of
spans removed earlier goes with them, and the disk cap deletes content first.

Capture also needs the application to emit content. Most SDKs only record
messages when you opt in (see `instrumentation.md`); Loupe cannot capture what
the SDK does not send.

## What counts as content

Content-key denylist: a fixed table of attribute keys, span events and log events
that carry prompts, completions, tool arguments and results, retrieved documents
and embedding input text. Matching attributes are removed from the stored
`attributes` JSON in every capture mode. The carriers are:

- span attributes: `gen_ai.input.messages`, `gen_ai.output.messages`,
  `gen_ai.system_instructions`, `gen_ai.tool.call.arguments`,
  `gen_ai.tool.call.result`, `gen_ai.prompt`, `gen_ai.completion`,
  `gen_ai.prompt.*`, `gen_ai.completion.*`, `input.value`, `output.value`,
  `llm.input_messages.*`, `llm.output_messages.*`, `llm.prompt_template.*`,
  `llm.prompts.*`, `tool_call.function.arguments`, `tool.parameters`,
  `retrieval.documents.*`, `reranker.*.documents.*`, `reranker.*_documents.*`,
  `embedding.embeddings.*.text`;
- span events named `gen_ai.*` (the older per-message events);
- GenAI log events, whose body and attributes are content: the per-message
  events (`gen_ai.system.message`, `gen_ai.user.message`,
  `gen_ai.assistant.message`, `gen_ai.tool.message`, `gen_ai.choice`) and
  `gen_ai.client.inference.operation.details`.

Log events are attached to the span named by their trace and span ids, even if
the span has not arrived yet. Content whose span never arrives is deleted after
`retention.orphan_content` (default 1h). Log records without span context, and
records that are not GenAI events, are discarded and counted
(`loupe_ingest_discarded_total{signal="logs",reason}`). Metrics are accepted and
discarded.

## Truncation

In `on` mode `content.max_bytes` (default 32 KiB) bounds the stored text of one
span: its content attributes, span events and all of its GenAI log events
together, whatever order and requests they arrive in. Text is cut at a UTF-8
boundary and the part is flagged truncated; parts after the budget is used up
are stored empty and flagged. Redaction runs first, so the limit applies to the
redacted text. The recorded size of each part is its original length.

## Redaction

`content.redact` is a list of presets and custom rules, applied before storage:

```yaml
content:
  capture: on
  redact:
    - email
    - api_key
    - { name: ticket, pattern: "TCK-[0-9]+", replacement: "[TICKET]" }
```

The default is `[email, api_key]`; an empty list turns redaction off. Presets
run first in the order listed, then custom rules in the order listed.

Redaction applies to stored content (`on` mode) and to the string values of
non-content span attributes in every mode, including nested values and before
the attribute is truncated to `receiver.max_attribute_bytes`. It does not apply
to span names, resource attributes or other typed fields; do not put secrets
there.

### Presets

| Preset | Replacement | What it matches |
|---|---|---|
| `email` | `[EMAIL]` | `[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}` |
| `phone` | `[PHONE]` | `\+\d{7,15}\|(?:\+\d{1,3}[ .\-]?)?(?:\(\d{1,4}\)[ .\-]?)?\d{2,4}(?:[ .\-]\d{2,4}){1,4}`, kept only if it has 7-15 digits, is not touching a word character, is not part of a dash-joined identifier such as a UUID, and is not an ISO date or an IPv4 address |
| `credit_card` | `[CREDIT_CARD]` | `\d(?:[ \-]?\d){12,18}`, kept only if the digits (13-19, spaces and dashes allowed) pass the Luhn check |
| `api_key` | `[API_KEY]` | `\bsk-(?:proj-\|ant-)?[A-Za-z0-9_\-]{20,}`, `\bgh[pos]_[A-Za-z0-9]{20,}`, `\bgithub_pat_[A-Za-z0-9_]{20,}`, `\bAKIA[0-9A-Z]{16}\b`, `\bxox[abprs]-[A-Za-z0-9\-]{10,}`, `\bAIza[0-9A-Za-z_\-]{35}`; also `Bearer <token>` (token of 20 or more characters), keeping the word `Bearer` |

Presets are heuristics: they miss formats they do not know and can match
look-alikes. They are a safety net, not a guarantee.

### Custom rules

A custom rule is `{name, pattern, replacement}`. `name` is required and unique,
`pattern` is a non-empty Go (RE2) regular expression, `replacement` may use
`$1`-style group references. Invalid rules, unknown presets and duplicate names
fail `loupe config validate` and startup with exit code 2. Test patterns against
real samples; a pattern that is too narrow leaks, one that is too broad destroys
data you wanted to keep.

## Custom application keys are not stripped

Only the denylisted keys are content. If your application puts a prompt, a
customer name or a secret in an attribute of its own (`app.prompt`,
`customer.email`), Loupe cannot know it is sensitive: the key is stored in the
`attributes` JSON in every capture mode, readable through the API and the
dashboard, and exported by `GET /v1/export/traces`. Protect such values with
redact rules (the values are masked, not the keys), or do not send them. The same
holds for span names, resource attributes, `user.id` and `session.id`.

## Deleting a user's data

```text
DELETE /v1/users/{user_id}/data
```

Requires the API token. Returns 202 with a `job_id`; follow it with
`GET /v1/admin/jobs/{id}`. The job runs on the writer in batches of
`retention.batch` rows, so ingest continues while it runs. It deletes:

- every trace in which the trace row or any span has that `user_id`, with all of
  its spans and content rows (a trace shared with other users is deleted whole);
- the user's rows in `sessions`;
- the user's rows in `rollup_user_hour` and `rollup_user_day`.

It then runs `wal_checkpoint(TRUNCATE)` so deleted pages do not survive in the
write-ahead log. The hourly and daily rollups (`rollup_hour`, `rollup_day`) keep
their aggregates: they have no user dimension, so they hold counts, tokens and
cost, not the user's identity.

Deletion removes the data from the live database only. It does **not** reach:

- backups in the `backups/` directory next to the database (`POST
  /v1/admin/backups`) and any file written by `loupe db backup`;
- the `<storage.path>.pre-v<N>` copies written before a schema migration;
- any copy you made yourself (volume snapshots, `loupe` exports, files from
  `GET /v1/export/traces`).

Delete or rotate those files according to your own retention rules. A new
trace for the same `user_id` can of course arrive later; stop the application
sending it, or drop it upstream.

## Where the data lives

One SQLite file, `storage.path`, plus its `-wal` and `-shm` files and the backup
copies above. Loupe sets `secure_delete=FAST`, so freed pages are overwritten
with zeros where SQLite can do so cheaply, but file-system snapshots and SSD wear
levelling are outside its control. Use disk encryption if the host is shared.
Access is controlled by tokens and loopback binding (see `operations.md`); put TLS in front
of any non-loopback listener.
