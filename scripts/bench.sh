#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Runs the Go micro-benchmarks and replaces the generated section of bench/RESULTS.md
# (between the BEGIN/END markers). The hand-maintained "Measured envelope" section
# and everything else in the file stay untouched.
results=bench/RESULTS.md
begin='<!-- BEGIN GENERATED: go-benchmarks -->'
end='<!-- END GENERATED: go-benchmarks -->'
if ! grep -rqs --include='*_test.go' '^func Benchmark' .; then
  echo "no benchmarks found; $results untouched"
  exit 0
fi
if ! grep -qF "$begin" "$results" || ! grep -qF "$end" "$results"; then
  echo "bench: markers missing in $results" >&2
  exit 1
fi
raw="$(mktemp)"
section="$(mktemp)"
out="$(mktemp)"
trap 'rm -f "$raw" "$section" "$out"' EXIT
go test -run '^$' -bench . -benchmem -count=3 ./... | tee "$raw"
{
  echo "Machine: $(uname -m) $(uname -s) $(uname -r); Go $(go env GOVERSION); generated $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  echo '```text'
  go tool benchstat "$raw"
  echo '```'
} > "$section"
awk -v begin="$begin" -v end="$end" -v section="$section" '
  $0 == begin { print; while ((getline line < section) > 0) print line; skip = 1; next }
  $0 == end { skip = 0 }
  !skip { print }
' "$results" > "$out"
cat "$out" > "$results"
