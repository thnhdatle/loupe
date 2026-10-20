# Triage contract fixtures

OTLP/JSON trace requests (app and Triage spans, one trace each) for the usage-dedupe golden test
`TestTriageContracts_*` in `internal/adapter/sqlite/triage_golden_test.go`. The rule is the
usage-duplicate rule in the attribute mapping of docs/guides/instrumentation.md.

The tests use these files when the Triage image is unavailable. With the image, re-record the
recorded ones from `examples/triage`:

```sh
scripts/record_triage.sh   # rewrites the recorded files only
```

| File | Origin | Span chain |
|---|---|---|
| `chat-single-attempt.json` | recorded, Triage v0.1.0 pre-release build | `chat triage-auto` (llm) -> `POST` -> `route request` (llm) -> `chat fake-small` |
| `chat-fallback.json` | recorded | same, attempts: `chat fake-small` (500, no usage), `chat fake-strong` |
| `chat-stream-continuation.json` | recorded | cut stream: `chat fake-small` (no usage), continuation `chat fake-strong` with the route's totals |
| `embeddings-chain.json` | recorded | `embeddings triage-embed` -> `POST` -> `route request` (embedding) -> `embeddings fake-embed` |
| `chat-stream-continuation-usage.json` | **hand-written** (derived from the recording) | as the cut stream, but both attempts report usage: 12/15 and 18/25 sum to the route's 30/40. The real Triage reports no usage on a cut attempt |
| `embeddings-under-chat-route.json` | **hand-written** (derived from the recording) | `chat triage-auto` -> `POST` -> `route request` (chat) -> `chat fake-small`, plus mf's `embeddings fake-embed` span (6 input tokens) as a child of `route request`. Needs Triage's `mf` strategy and its model files, which the example does not run |

Hand-written files reuse the recorded spans with new ids and changed usage. Their span chains are
those Triage emits. `scripts/record_triage.sh` never touches them.
