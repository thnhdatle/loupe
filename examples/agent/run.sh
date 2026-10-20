#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Runs the example against already running Loupe and fakellm, then prints the trace
# Loupe stored, fetched from its REST API. See README.md for the environment.
#   LOUPE_OTLP_ENDPOINT  OTLP/HTTP base URL   (default http://127.0.0.1:4318)
#   LOUPE_API            REST API base URL    (default http://127.0.0.1:3000)
#   FAKELLM_URL          OpenAI-compatible API (default http://127.0.0.1:8090/v1)
# #   EXAMPLE_NO_SUMMARY=1 skip the summary (used when recording payloads)
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
root=../..

export FAKELLM_URL="${FAKELLM_URL:-http://127.0.0.1:8090/v1}"
OTEL_SERVICE_NAME="example-agent-$(date +%s)-$$"
export OTEL_SERVICE_NAME
export OTEL_EXPORTER_OTLP_ENDPOINT="${LOUPE_OTLP_ENDPOINT:-http://127.0.0.1:4318}"
export OTEL_EXPORTER_OTLP_PROTOCOL="${OTEL_EXPORTER_OTLP_PROTOCOL:-http/protobuf}"
export OTEL_TRACES_EXPORTER=otlp OTEL_METRICS_EXPORTER=otlp OTEL_LOGS_EXPORTER=otlp
# Content opt-in: the latest GenAI conventions, content as gen_ai.client.inference.operation.details
# log events.
export OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental
export OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=event_only

uv run --frozen --project "$root/tests/python" python app.py

[[ -z "${EXAMPLE_NO_SUMMARY:-}" ]] || exit 0
exec go run "$root/bench/cmd/exsummary" -min-spans 4 -content "$OTEL_SERVICE_NAME"
