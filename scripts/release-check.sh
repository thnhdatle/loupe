#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Pre-release checks, run by `make release-check` before tagging.
rc=0
fail() { echo "FAIL: $*"; rc=1; }

for f in LICENSE NOTICE README.md CHANGELOG.md ROADMAP.md CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md; do
  [[ -f "$f" ]] || fail "missing community file $f"
done
for f in .github/CODEOWNERS .github/dependabot.yml .github/PULL_REQUEST_TEMPLATE.md; do
  [[ -f "$f" ]] || fail "missing $f"
done

# Only project files are tracked: hidden directories other than .github and
# .githooks, and top-level Markdown files outside this list, are not.
extra="$(git ls-files | grep -E '^\.[^/]+/' | grep -vE '^\.(github|githooks)/' || true)"
extra+="$(git ls-files '*.md' | grep -v / |
  grep -vE '^(README|CHANGELOG|CONTRIBUTING|CODE_OF_CONDUCT|SECURITY|ROADMAP)\.md$' || true)"
[[ -z "$extra" ]] || fail "unexpected files tracked: $extra"

# Every commit: a one-line Conventional Commit message.
re='^(feat|fix|perf|refactor|test|docs|build|ci|chore)(\([a-z0-9-]+\))?!?: [a-z0-9].{0,70}[^.]$'
while IFS= read -r sha; do
  body="$(git log -1 --format=%B "$sha")"
  if [[ "$(printf '%s\n' "$body" | wc -l | tr -d ' ')" != 1 ]]; then
    fail "commit $sha has a multi-line message"
  elif ! [[ "$body" =~ $re ]]; then
    fail "commit $sha message is not a Conventional Commit: $body"
  fi
done < <(git rev-list HEAD)

# SPDX header in every Go and shell source file.
while IFS= read -r f; do
  head -3 "$f" | grep -q 'SPDX-License-Identifier: Apache-2.0' || fail "no SPDX header: $f"
done < <(git ls-files '*.go' '*.sh' '.githooks/*')

# TODO must be TODO(roadmap) and listed in ROADMAP.md.
if git grep -n 'TODO' -- '*.go' '*.sh' | grep -v 'TODO(roadmap)' | grep -v release-check.sh; then
  fail "bare TODO found"
fi

# Acceptance report must exist and have no FAIL rows.
if [[ -f acceptance/report.md ]]; then
  if grep -q '| FAIL |' acceptance/report.md; then fail "acceptance/report.md has FAIL rows"; fi
else
  fail "acceptance/report.md missing"
fi

[[ -f bench/RESULTS.md ]] || fail "bench/RESULTS.md missing"

# The release section of the changelog carries its date.
grep -Eq '^## \[[0-9]+\.[0-9]+\.[0-9]+\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$' CHANGELOG.md ||
  fail "CHANGELOG.md has no dated release section (## [x.y.z] - YYYY-MM-DD)"
[[ -z "$(git status --porcelain)" ]] || echo "note: working tree is not clean"
exit "$rc"
