#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Runs every Fuzz target for FUZZTIME (default 30s).
FUZZTIME="${FUZZTIME:-30s}"
found=0
while IFS= read -r file; do
  dir="$(dirname "$file")"
  while IFS= read -r target; do
    found=1
    echo "== $dir $target"
    go test "./$dir" -run '^$' -fuzz "^${target}\$" -fuzztime "$FUZZTIME"
  done < <(grep -ho '^func Fuzz[A-Za-z0-9_]*' "$file" | sed 's/^func //')
done < <(grep -rl --include='*_test.go' '^func Fuzz' . || true)
((found)) || echo "no fuzz targets found"
