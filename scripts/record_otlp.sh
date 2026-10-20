#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Records the raw OTLP requests each examples/<name> run sends into
# testdata/otlp/<family>/recorded-<variant>-<signal>-N.json, using the tiny capture
# server in bench/cmd/otlpcapture instead of Loupe. Payloads are normalised to OTLP/JSON
# (hex ids); the Node example exports OTLP/JSON, the others protobuf. Hand-written payloads
# (handwritten-*.json) are never touched.
# Needs fakellm (FAKELLM_URL, default http://127.0.0.1:8090/v1), uv, and node with
# `npm ci` run in tests/node. Afterwards refresh the expected output with
#   go test ./internal/adapter/otlp -run Golden -update
#   RECORD_ADDR  capture listen address (default 127.0.0.1:14399)
export FAKELLM_URL="${FAKELLM_URL:-http://127.0.0.1:8090/v1}"
addr="${RECORD_ADDR:-127.0.0.1:14399}"
tmp="$(mktemp -d)"
go build -o "$tmp/otlpcapture" ./bench/cmd/otlpcapture
pid=""
cleanup() {
  [[ -z "$pid" ]] || kill "$pid" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

# record <family> <variant> <example dir> [VAR=value ...]
record() {
  local family="$1" variant="$2" example="$3"
  shift 3
  local dir="testdata/otlp/$family"
  mkdir -p "$dir"
  find "$dir" -name "recorded-$variant-*.json" ! -name "*.golden.json" -delete
  "$tmp/otlpcapture" -dir "$dir" -prefix "recorded-$variant-" -addr "$addr" 2>"$tmp/capture.log" &
  pid=$!
  for _ in $(seq 50); do
    (: >"/dev/tcp/${addr%:*}/${addr#*:}") 2>/dev/null && break
    sleep 0.1
  done
  echo "== record $family/$variant"
  env "$@" LOUPE_OTLP_ENDPOINT="http://$addr" \
    EXAMPLE_NO_SUMMARY=1 "examples/$example/run.sh" >/dev/null
  kill "$pid"
  wait "$pid" 2>/dev/null || true
  pid=""
  find "$dir" -name "recorded-$variant-*.json" ! -name "*.golden.json" | sort
}

record openai-v2 events python-openai OPENAI_V2_CONTENT_FORM=event
record openai-v2 span python-openai OPENAI_V2_CONTENT_FORM=span
record openinference default python-openinference
record node-openai default node-openai
record go-manual default go-manual
record agent default agent
