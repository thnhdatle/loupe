#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
# Converts LiteLLM's model_prices_and_context_window.json into Loupe pricing YAML
# (docs/guides/pricing.md). Reads the file named by the argument, or stdin.
#
#   scripts/litellm_to_pricing.sh [-d YYYY-MM-DD] [-n] [file] > prices.yaml
#
#   -d DATE  effective_from and as_of (UTC midnight); default: today (UTC)
#   -n       omit provider so entries match every provider
#
# Per-token costs are converted to USD per million tokens by shifting the decimal
# point of the shortest decimal text of the JSON number, never by float arithmetic.
# Limitation: LiteLLM stores a double, so the text is whatever the shortest
# round-trip form of that double is; prices that need more than 9 fractional
# digits per Mtok are truncated (with a warning on stderr).

url="https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
date="$(date -u +%Y-%m-%d)"
noprovider=false
while getopts "d:nh" opt; do
  case "$opt" in
    d) date="$OPTARG" ;;
    n) noprovider=true ;;
    *)
      sed -n '5,9p' "$0" | sed 's/^# \{0,1\}//' >&2
      exit 2
      ;;
  esac
done
shift $((OPTIND - 1))
if [[ ! "$date" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
  echo "litellm_to_pricing: date must be YYYY-MM-DD: $date" >&2
  exit 2
fi
if (($# > 1)); then
  echo "litellm_to_pricing: at most one input file" >&2
  exit 2
fi

# shellcheck disable=SC2016 # $-names below are jq variables.
filter='
def zeros($n): if $n <= 0 then "" else "0" * $n end;

# Decimal text of a per-token cost, multiplied by 10^6 by moving the point.
def per_mtok:
  (tojson | ascii_downcase
   | capture("^(?<m>[0-9]*\\.?[0-9]*)(e(?<e>[+-]?[0-9]+))?$")) as $c
  | ($c.m | index(".")) as $dot
  | ($c.m | gsub("\\."; "")) as $digits
  | (($dot // ($c.m | length)) + (($c.e // "0") | tonumber) + 6) as $pp
  | (if $pp <= 0 then {i: "0", f: (zeros(0 - $pp) + $digits)}
     elif $pp >= ($digits | length) then {i: ($digits + zeros($pp - ($digits | length))), f: ""}
     else {i: $digits[0:$pp], f: $digits[$pp:]} end) as $s
  | ($s.i | sub("^0+"; "") | if . == "" then "0" else . end) as $i
  | ($s.f | sub("0+$"; "")) as $f
  | (if ($f | length) > 9 then
       (("warning: price truncated to 9 decimals: " + $c.m + "e" + ($c.e // "0") + "\n") | stderr) as $_ | $f[0:9] | sub("0+$"; "")
     else $f end) as $f2
  | if $f2 == "" then $i else $i + "." + $f2 end;

def num($k): if (.[$k] | type) == "number" and .[$k] >= 0 then .[$k] else null end;

"prices:",
( to_entries | sort_by(.key)[]
  | select(.key != "sample_spec" and (.value | type) == "object")
  | .key as $model | .value
  | select(num("input_cost_per_token") != null)
  | "  - match: exact",
    (if $noprov or ((.litellm_provider // "") == "") then empty
     else "    provider: " + (.litellm_provider | tojson) end),
    "    model: " + ($model | tojson),
    "    input_per_mtok: " + (num("input_cost_per_token") | per_mtok),
    "    output_per_mtok: " + ((num("output_cost_per_token") // 0) | per_mtok),
    (num("cache_read_input_token_cost") | if . == null then empty else "    cache_read_per_mtok: " + per_mtok end),
    (num("cache_creation_input_token_cost") | if . == null then empty else "    cache_write_per_mtok: " + per_mtok end),
    (num("output_cost_per_reasoning_token") | if . == null then empty else "    reasoning_per_mtok: " + per_mtok end),
    "    effective_from: " + $date + "T00:00:00Z",
    "    as_of: " + $date + "T00:00:00Z",
    "    source: " + ($url | tojson)
)
'

args=(-r --arg date "$date" --arg url "$url" --argjson noprov "$noprovider" "$filter")
if (($# == 1)); then
  jq "${args[@]}" "$1"
else
  jq "${args[@]}"
fi
