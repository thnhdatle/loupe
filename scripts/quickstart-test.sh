#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Runs the README quickstart in a fresh clone.
if [[ ! -f README.md ]] || ! grep -q '^## Quickstart' README.md; then
  echo "README.md has no Quickstart section"
  exit 1
fi
tmp="$(mktemp -d)"
cleanup() {
  [[ -n "${QUICKSTART_KEEP:-}" ]] || docker compose -p loupe down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT
git clone --quiet . "$tmp/loupe"
cd "$tmp/loupe"
docker build -q -t thnhdatle/loupe:dev .
export RAYLD_LOUPE_IMAGE=thnhdatle/loupe:dev
export RAYLD_FAKELLM_IMAGE="${RAYLD_FAKELLM_IMAGE:-ghcr.io/thnhdatle/fakellm:v0.1.0}"
awk '/^## Quickstart/ {on=1; next} /^## / {on=0} on' README.md |
  awk '/^```bash/ {c=1; next} /^```/ {c=0} c' > "$tmp/quickstart.sh"
if [[ ! -s "$tmp/quickstart.sh" ]]; then
  echo "no bash blocks in README Quickstart"
  exit 1
fi
bash -euo pipefail "$tmp/quickstart.sh"
