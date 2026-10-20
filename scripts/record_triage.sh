#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Records the OTLP/JSON traces that examples/triage sends (the app's spans and the spans of the
# real Triage image) into testdata/contracts/triage/<scenario>.json, one file per trace, using
# the capture server in bench/cmd/otlpcapture instead of Loupe. The other fixtures in that
# directory are hand-written (see its README.md) and never touched.
# Needs fakellm (FAKELLM_URL, default http://127.0.0.1:8090/v1), the Triage image
# (RAYLD_TRIAGE_IMAGE, default ghcr.io/thnhdatle/triage:v0.1.0), docker, uv and python3.
#   RECORD_ADDR  capture listen address (default 127.0.0.1:14399)
#   TRIAGE_PORT  host port of Triage    (default 8080)
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

go build -o "$tmp/otlpcapture" ./bench/cmd/otlpcapture
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
