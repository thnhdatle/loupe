#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Runs the example against a running Loupe and fakellm and prints the stored trace.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
root=../..

export FAKELLM_URL="${FAKELLM_URL:-http://127.0.0.1:8090/v1}"
OTEL_SERVICE_NAME="example-agent-$(date +%s)-$$"
export OTEL_SERVICE_NAME
export OTEL_EXPORTER_OTLP_ENDPOINT="${LOUPE_OTLP_ENDPOINT:-http://127.0.0.1:4318}"
export OTEL_EXPORTER_OTLP_PROTOCOL="${OTEL_EXPORTER_OTLP_PROTOCOL:-http/protobuf}"
export OTEL_TRACES_EXPORTER=otlp OTEL_METRICS_EXPORTER=otlp OTEL_LOGS_EXPORTER=otlp
export OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
export OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=event_only

uv run --frozen --project "$root/tests/python" python app.py

[[ -z "${EXAMPLE_NO_SUMMARY:-}" ]] || exit 0
exec go run "$root/scripts/exsummary" -min-spans 4 -content "$OTEL_SERVICE_NAME"
