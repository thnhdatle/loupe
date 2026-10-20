# Pricing

Loupe computes the cost of every `llm` and `embedding` span at ingest from a
price table. The table is the embedded `pricing/default.yaml` plus an optional
user file (`pricing.file`) merged on top. Costs are stored as integer
`cost_nanos` (nano-USD); the API also returns `cost_usd` for display.

The default prices are a hand-maintained snapshot of public list prices, not a
live feed. Check the `as_of` and `source` of every entry you rely on, and
override anything that matters to your bill (negotiated rates, regional or batch
pricing, fine-tuned models).

## Price file format

A file is a YAML mapping with one key, `prices`, holding a list of entries.
Unknown keys are errors. Prices are written as plain decimal text (USD per
million tokens), at most 9 fractional digits, no sign, no exponent, and are never
read as floating point: write `0.075`, not `7.5e-2`. Quote nothing; YAML
`0.30` and `0.3` are the same price.

```yaml
prices:
  - match: exact
    provider: openai
    model: gpt-4o
    input_per_mtok: 2.50
    output_per_mtok: 10.00
    cache_read_per_mtok: 1.25
    effective_from: 2024-10-01T00:00:00Z
    as_of: 2026-10-01T00:00:00Z
    source: "https://openai.com/api/pricing"
```

| Field | Required | Meaning |
|---|---|---|
| `match` | yes | `exact` (byte for byte, case-sensitive) or `glob` |
| `model` | yes | Model id (`exact`) or pattern (`glob`) compared with the span's pricing model |
| `provider` | no | Applies only to spans of that provider (case-insensitive); empty applies to every provider |
| `input_per_mtok` | yes | USD per million input tokens (all input, including cache read and cache write) |
| `output_per_mtok` | yes | USD per million output tokens (including reasoning) |
| `cache_read_per_mtok` | no | Price of cache-read tokens; defaults to `input_per_mtok` |
| `cache_write_per_mtok` | no | Price of cache-write tokens; defaults to `input_per_mtok` |
| `reasoning_per_mtok` | no | Price of reasoning tokens; defaults to `output_per_mtok` |
| `effective_from` | yes | First instant at which this version applies: RFC 3339 timestamp or `YYYY-MM-DD` (UTC midnight) |
| `as_of` | default file: yes; user file: no | When the price was last checked against `source`; drives the age shown by `pricing list` |
| `source` | default file: yes; user file: no | Where the price comes from (URL or note) |
| `id` | no | Free-text label, ignored |

Glob syntax: `*` matches any run of characters (including `/` and empty), `?`
matches one character, everything else matches itself. There are no character
classes and no escapes. Matching is anchored at both ends.

The span's pricing model is its response model, falling back to its request
model (see the attribute mapping in `instrumentation.md`); the provider is the
span's normalised provider. A span with no model is never priced. A non-empty
response model that has no price is not retried with the request model: the
span is unpriced. Providers are compared case-insensitively (`OpenAI` and
`openai` are one provider), models case-sensitively.

## Merging the default and the user file

Entries with the same `(match, provider, model)` form a series, and each entry
in it is one version of that series, distinguished by `effective_from`. Two
versions of a series with the same `effective_from` are an error.

A user series replaces the default series with the same `(match, provider,
model)` entirely: none of the default versions of that series remain, so a user
entry that starts in 2026 leaves earlier spans of that model unpriced (unless another series matches)
unless you also list the earlier versions. Series that exist in only one file are all kept.

## Which entry prices a span

Only series with a version whose `effective_from` is at or before the span's
start time are candidates. Among them:

1. A user series beats any default series, before specificity is compared. A
   user glob therefore beats a default exact entry.
2. Within one origin, an `exact` series beats any glob; a glob with more literal
   characters beats one with fewer; at equal specificity a provider-specific
   series beats a provider-less one. Remaining ties go to the pattern, then the
   provider, that sorts first.
3. The version used is the one with the latest `effective_from` that is not after
   the span's start time.

Spans are priced by their start time, so a price change with a new
`effective_from` affects only spans that start after it.

## Cost formula

Prices are converted to integer nano-USD per Mtok (USD x 10^9). With `input`,
`output`, `cache_read`, `cache_write` and `reasoning` token counts (input
includes cache tokens, output includes reasoning tokens):

```text
uncached_in = max(0, input - cache_read - cache_write)
plain_out   = max(0, output - reasoning)
numerator   = uncached_in*P_in + cache_read*(P_cache_read ?? P_in)
            + cache_write*(P_cache_write ?? P_in)
            + plain_out*P_out + reasoning*(P_reasoning ?? P_out)
cost_nanos  = round_half_up(numerator / 10^6)
```

The numerator is exact (128-bit); there is one rounding per span, so sub-micro-dollar
parts of a span are not rounded away one by one. When a `max(0, ...)` clamps (the reported
cache or reasoning tokens exceed their total) the span gets the `usage_clamped`
flag. Costs are integers so that totals do not depend on the order in which
spans are summed and `pricing recompute` deltas cancel exactly. A priced span
with no usage costs 0; a cost above 2^63-1 nano-USD (about 9.2 billion USD) for
one span is reported as an overflow. Prices finer than one nano-USD per million
tokens cannot be expressed.

## Unpriced spans

A span with tokens but no matching price gets `cost_nanos = NULL`. It is counted
in `loupe_unpriced_spans_total{model}` (the label keeps the top 50 models, the
rest are `other`), in the per-rollup unpriced count, and shown as "unpriced" in
the dashboard. Totals sum only priced spans, so they understate the cost until
you add a price and run `loupe pricing recompute`.

## Inspecting the table

```text
loupe pricing list [--json] [--config loupe.yaml]
```

Prints every entry in effect after the merge: origin (`default` or `user`),
match, provider (`*` for none), model, the five prices, `effective_from`,
`as_of`, age in days since `as_of`, and `source`. Entries whose `as_of` is older
than 90 days are marked `STALE`. `--json` adds `age_days` and `stale` fields.

## Repricing stored spans

Prices are applied at ingest, and the server reads the price files at startup:
restart it after editing `pricing.file` so new spans use the new table. Then
reprice history:

```text
loupe pricing recompute --since 2026-09-01 [--until 2026-10-01]
loupe pricing recompute --since 2026-09-01 --offline
```

`--since` is required and `--until` is exclusive; both accept an RFC 3339
timestamp or `YYYY-MM-DD` (UTC), and select spans by start time. Without
`--offline` the command calls the admin API of the running server
(`POST /v1/admin/pricing/recompute`, API token required; `--server` and
`--token-file` override the defaults) and waits for the job. The server runs it as
writer jobs of `retention.batch` spans each, so ingest continues between jobs.
Span, trace, session and rollup cost sums change by the delta. With `--offline`
the command takes the database lock itself, so the server must be stopped.

An online recompute makes the server re-read the price files from disk, so it
works even before a restart; `--offline` loads the table from the `pricing.file`
of the config you pass, so use the same config as the server.

## Converting LiteLLM's price list

LiteLLM publishes a large community price list,
`model_prices_and_context_window.json`. Convert it with
`scripts/litellm_to_pricing.sh` (needs `bash` and `jq`):

```text
curl -fsSL https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json -o litellm.json
scripts/litellm_to_pricing.sh -d 2026-10-19 litellm.json > prices.yaml
```

The script reads the file named by its argument or stdin and writes YAML on
stdout. Options: `-d YYYY-MM-DD` sets `effective_from` and `as_of` (default
today, UTC); `-n` omits `provider` so entries match any provider. Each model with
an `input_cost_per_token` becomes an `exact` entry: `provider` from
`litellm_provider`, `model` from the JSON key, `input_per_mtok`,
`output_per_mtok` (0 when absent, as for embedding models) and, when present,
`cache_read_per_mtok` (`cache_read_input_token_cost`), `cache_write_per_mtok`
(`cache_creation_input_token_cost`) and `reasoning_per_mtok`
(`output_cost_per_reasoning_token`); `source` is the LiteLLM URL.

Limitations:

- Per-token costs are multiplied by 10^6 by moving the decimal point of the
  number's shortest decimal text, so there are no float artefacts, but LiteLLM
  stores doubles: a value needing more than 9 decimals per Mtok is truncated and
  reported on stderr.
- LiteLLM provider names (`azure`, `bedrock`, `vertex_ai-language-models`) often
  differ from the `gen_ai.provider.name` your SDK emits (`azure.ai.openai`,
  `aws.bedrock`). A provider-specific entry only applies when the names match, so
  check with a real trace or use `-n`.
- LiteLLM model ids sometimes carry a provider prefix (`azure/gpt-4o`) or a
  tiered price that Loupe does not model; the script copies the base price only.
- Review the output before use: it is a third-party snapshot with the same
  caveats as the defaults.

Use the result as `pricing.file`, then check it with `loupe pricing list`:

```yaml
pricing:
  file: /etc/loupe/prices.yaml
```

Because a user series replaces the default series with the same key, a full
LiteLLM import replaces any default entry it also contains, and the converted
entries have a single version with `effective_from` of the conversion date, so
spans that started earlier are unpriced by them (use an earlier `-d` date to
cover history).
