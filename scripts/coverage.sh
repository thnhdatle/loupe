#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Merged coverage (unit + integration tag): >= 80% overall, >= 90% for domain and app.
WHOLE_MIN="${COVERAGE_MIN:-80}"
CORE_MIN="${COVERAGE_CORE_MIN:-90}"
profile=coverage.out

go test -race -tags integration -count=1 -covermode=atomic -coverpkg=./... -coverprofile="$profile" ./...

# stats <include-regex> <exclude-regex>  ->  "<covered> <total>" statements
stats() {
  awk -v inc="$1" -v exc="$2" '
    NR == 1 { next }
    {
      split($1, a, ":")
      if (inc != "" && a[1] !~ inc) next
      if (exc != "" && a[1] ~ exc) next
      # -coverpkg repeats every block once per test binary: merge by block.
      stmts[$1] = $2
      if ($3 > 0) hit[$1] = 1
    }
    END {
      for (b in stmts) { total += stmts[b]; if (b in hit) covered += stmts[b] }
      printf "%d %d\n", covered, total
    }' "$profile"
}

pct() { # covered total
  if (($2 == 0)); then echo "n/a"; else awk -v c="$1" -v t="$2" 'BEGIN { printf "%.1f", 100 * c / t }'; fi
}

excl='/(cmd|bench|acceptance|examples|research)/|\.pb\.go|_gen\.go|\.gen\.go'
read -r wc wt < <(stats "" "$excl")
read -r cc ct < <(stats '/internal/(domain|app)/' "")

rc=0
wp="$(pct "$wc" "$wt")"
cp="$(pct "$cc" "$ct")"
echo "coverage whole repo: $wp% ($wc/$wt statements, min $WHOLE_MIN%)"
echo "coverage domain+app: $cp% ($cc/$ct statements, min $CORE_MIN%)"

if [[ "$wp" != "n/a" ]] && awk -v p="$wp" -v m="$WHOLE_MIN" 'BEGIN { exit !(p < m) }'; then rc=1; fi
if [[ "$cp" != "n/a" ]] && awk -v p="$cp" -v m="$CORE_MIN" 'BEGIN { exit !(p < m) }'; then rc=1; fi
exit "$rc"
