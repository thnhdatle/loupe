# Pricing

Set the prices Loupe uses to compute the cost of `llm` and `embedding` spans.

Loupe uses the embedded `pricing/default.yaml`, with your `pricing.file` merged on top.
Costs are stored in nano-USD (`cost_nanos`); the API also returns `cost_usd`.

The default prices are a hand-maintained snapshot, not a live feed. Check the `as_of` and `source`
of entries you rely on. Override anything that matters to your bill (negotiated, regional or batch
rates, fine-tuned models).

## Price file format

```yaml
prices:
  - match: exact                      # or glob
    provider: openai                  # optional
    model: gpt-4o
    input_per_mtok: 2.50              # USD per million tokens
    output_per_mtok: 10.00
    cache_read_per_mtok: 1.25         # optional
    effective_from: 2024-10-01T00:00:00Z
    as_of: 2026-10-01T00:00:00Z
    source: "https://openai.com/api/pricing"
```

Write prices as plain decimals, at most 9 fractional digits: `0.075`, not `7.5e-2`.
Unknown keys are errors.

| Field | Required | Meaning |
|---|---|---|
| `match` | yes | `exact` (case-sensitive) or `glob` |
| `model` | yes | Model id or glob pattern |
| `provider` | no | Only this provider (case-insensitive); empty = any |
| `input_per_mtok` | yes | All input tokens, cache included |
| `output_per_mtok` | yes | All output tokens, reasoning included |
| `cache_read_per_mtok` | no | Cache-read tokens; default `input_per_mtok` |
| `cache_write_per_mtok` | no | Cache-write tokens; default `input_per_mtok` |
| `reasoning_per_mtok` | no | Reasoning tokens; default `output_per_mtok` |
| `effective_from` | yes | Start: RFC 3339 or `YYYY-MM-DD` (UTC) |
| `as_of` | default file: yes; user file: no | Last checked against `source` |
| `source` | default file: yes; user file: no | URL or note |
| `id` | no | Label, ignored |

Glob: `*` matches any characters (including `/`), `?` matches one. The whole model must match.

The span's model is its response model, else its request model. A span with no model is never priced.
If the response model has no price, the span is unpriced: the request model is not tried.

## Merging the default and the user file

A user entry replaces every default version with the same `match`, `provider` and `model`.
So a user entry starting in 2026 leaves earlier spans of that model unpriced, unless you also
list the earlier versions.

## Which entry prices a span

1. A user entry beats a default entry.
2. `exact` beats glob. A glob with more literal characters beats one with fewer. A provider-specific entry beats a provider-less one.
3. The version used is the latest `effective_from` not after the span's start.

A price change affects only spans that start after its `effective_from`.

## Cost formula

```text
uncached_in = max(0, input - cache_read - cache_write)
plain_out   = max(0, output - reasoning)
numerator   = uncached_in*P_in + cache_read*(P_cache_read ?? P_in)
            + cache_write*(P_cache_write ?? P_in)
            + plain_out*P_out + reasoning*(P_reasoning ?? P_out)
cost_nanos  = round_half_up(numerator / 10^6)
```

If cache or reasoning tokens exceed their total, the span is flagged `usage_clamped`.

## Unpriced spans

A span with tokens but no price has no cost. It shows as "unpriced" and is counted in
`loupe_unpriced_spans_total{model}`. Totals understate cost until you add a price and run
`loupe pricing recompute`.

## Inspecting the table

```sh
loupe pricing list [--json] [--config loupe.yaml]   # every entry in effect
```

Entries with `as_of` older than 90 days are marked `STALE`.

## Repricing stored spans

Restart the server after editing `pricing.file`, so new spans use it. Then reprice history:

```sh
loupe pricing recompute --since 2026-09-01 [--until 2026-10-01]   # through the running server
loupe pricing recompute --since 2026-09-01 --offline              # server stopped
```

- `--since` is required. `--until` is exclusive. Both take RFC 3339 or `YYYY-MM-DD` (UTC).
- Online needs the API token. `--server` and `--token-file` override the defaults. Ingest continues.
- `--offline` uses the `pricing.file` of the config you pass. Use the server's config.

## Converting LiteLLM's price list

```sh
curl -fsSL https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json -o litellm.json
scripts/litellm_to_pricing.sh -d 2026-10-19 litellm.json > prices.yaml   # needs bash and jq
loupe pricing list                                                        # check the result
```

| Option | Effect |
|---|---|
| `-d YYYY-MM-DD` | `effective_from` and `as_of` (default: today, UTC) |
| `-n` | omit `provider`, so entries match any provider |

Then use it:

```yaml
pricing:
  file: /etc/loupe/prices.yaml
```

Review the output before use:

- LiteLLM provider names (`azure`, `bedrock`) often differ from what your SDK sends
  (`azure.ai.openai`, `aws.bedrock`). Check with a real trace, or use `-n`.
- Tiered prices are not supported. Only the base price is copied.
- Values needing more than 9 decimals per Mtok are truncated and reported on stderr.
- The import replaces matching default entries. Its prices start at the `-d` date, so use an
  earlier date to cover older spans.
