#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Runs each examples/<name>/run.sh and checks its output against expected.txt.
shopt -s nullglob
dirs=(examples/*/)
if ((${#dirs[@]} == 0)); then
  echo "no examples found"
  exit 0
fi
# Starts Loupe and fakellm from deploy/docker-compose.yml (host ports can be moved with
# LOUPE_*_HOST_PORT / FAKELLM_HOST_PORT plus LOUPE_OTLP_ENDPOINT, LOUPE_API, FAKELLM_URL) and
# removes them afterwards.
compose=(docker compose -f deploy/docker-compose.yml)
export RAYLD_LOUPE_IMAGE="${RAYLD_LOUPE_IMAGE:-thnhdatle/loupe:dev}"
trap '"${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true' EXIT
"${compose[@]}" up -d --build --wait >/dev/null
LOUPE_API_TOKEN="$(docker cp "$("${compose[@]}" ps -q loupe)":/data/api_token - | tar -xO)"
export LOUPE_API_TOKEN
rc=0
for d in "${dirs[@]}"; do
  d="${d%/}"
  if [[ ! -x "$d/run.sh" || ! -f "$d/expected.txt" ]]; then
    echo "FAIL $d: needs executable run.sh and expected.txt"
    rc=1
    continue
  fi
  echo "== $d"
  if ! diff <("$d/run.sh") "$d/expected.txt"; then rc=1; fi
done
exit "$rc"
