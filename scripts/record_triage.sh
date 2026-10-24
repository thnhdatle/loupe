#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Records the OTLP/JSON traces of the triage example with the capture server.
export FAKELLM_URL="${FAKELLM_URL:-http://127.0.0.1:8090/v1}"
addr="${RECORD_ADDR:-127.0.0.1:14399}"
dir="testdata/contracts/triage"
tmp="$(mktemp -d)"
pid=""
cleanup() {
  [[ -z "$pid" ]] || kill "$pid" 2>/dev/null || true
  find "$tmp" -type f -delete
  find "$tmp" -type d -empty -delete
}
trap cleanup EXIT

go build -o "$tmp/otlpcapture" ./scripts/otlpcapture
"$tmp/otlpcapture" -dir "$tmp" -prefix "r-" -addr "$addr" 2>"$tmp/capture.log" &
pid=$!
for _ in $(seq 50); do
  (: >"/dev/tcp/${addr%:*}/${addr#*:}") 2>/dev/null && break
  sleep 0.1
done
echo "== record triage"
LOUPE_OTLP_ENDPOINT="http://$addr" EXAMPLE_NO_SUMMARY=1 examples/triage/run.sh >/dev/null
kill "$pid"
wait "$pid" 2>/dev/null || true
pid=""

mkdir -p "$dir"
python3 -I scripts/split_triage.py "$tmp" "$dir"
