#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Mutation testing (Gremlins) on the core packages below; efficacy >= 60%.
THRESHOLD="${MUTATION_THRESHOLD:-60}"
packages=(
  internal/domain/semconv
  internal/domain/content
  internal/domain/cost
  internal/domain/rollup
  internal/app/writer
)
rc=0
for p in "${packages[@]}"; do
  code="$(find "$p" -maxdepth 1 -name '*.go' ! -name '*_test.go' ! -name doc.go | head -1)"
  if [[ -z "$code" ]]; then
    echo "FAIL $p: no Go code to mutate"
    rc=1
    continue
  fi
  echo "== $p"
  go tool gremlins unleash --threshold-efficacy "$THRESHOLD" "./$p" || rc=1
done
exit "$rc"
