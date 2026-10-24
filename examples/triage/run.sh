#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Runs the example with Triage in front of fakellm and prints the stored traces.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
root=../..

export FAKELLM_URL="${FAKELLM_URL:-http://127.0.0.1:8090/v1}"
otlp="${LOUPE_OTLP_ENDPOINT:-http://127.0.0.1:4318}"
port="${TRIAGE_PORT:-8080}"
image="${RAYLD_TRIAGE_IMAGE:-ghcr.io/thnhdatle/triage:v0.1.0}"

from_container() { sed -E 's#//(127\.0\.0\.1|localhost)([:/]|$)#//host.docker.internal\2#' <<<"$1"; }

tmp="$(mktemp -d)"
name="loupe-example-triage-$$"
cleanup() {
  docker stop -t 10 "$name" >/dev/null 2>&1 || true
  rm -f "$tmp/config.yaml"
  rmdir "$tmp"
}
trap cleanup EXIT

chmod 755 "$tmp"
sed "s#FAKELLM_BASE_URL#$(from_container "${FAKELLM_URL%/}")#" triage.yaml >"$tmp/config.yaml"
chmod 644 "$tmp/config.yaml"
docker run -d --rm --name "$name" --add-host host.docker.internal:host-gateway \
  -p "127.0.0.1:$port:8080" -v "$tmp/config.yaml:/etc/triage/config.yaml:ro" \
  -e OTEL_SERVICE_NAME=triage \
  -e OTEL_EXPORTER_OTLP_ENDPOINT="$(from_container "$otlp")" \
  -e OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
  -e OTEL_BSP_SCHEDULE_DELAY=200 \
  "$image" serve --config /etc/triage/config.yaml >/dev/null
for _ in $(seq 100); do
  curl -fsS "http://127.0.0.1:$port/v1/models" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -fsS "http://127.0.0.1:$port/v1/models" >/dev/null

OTEL_SERVICE_NAME="example-triage-$(date +%s)-$$"
export OTEL_SERVICE_NAME
export OTEL_EXPORTER_OTLP_ENDPOINT="$otlp"
export OTEL_EXPORTER_OTLP_PROTOCOL="${OTEL_EXPORTER_OTLP_PROTOCOL:-http/protobuf}"
export OTEL_TRACES_EXPORTER=otlp OTEL_METRICS_EXPORTER=otlp OTEL_LOGS_EXPORTER=otlp
export OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
export OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=event_only
export TRIAGE_URL="http://127.0.0.1:$port/v1"
export FAKELLM_CONTROL_URL="${FAKELLM_URL%/v1}"

uv run --frozen --project "$root/tests/python" python app.py

if [[ -n "${EXAMPLE_NO_SUMMARY:-}" ]]; then
  exit 0
fi
go run "$root/scripts/exsummary" -min-spans 4 -traces 4 "$OTEL_SERVICE_NAME"
