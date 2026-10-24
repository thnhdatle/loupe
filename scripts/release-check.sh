#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Runs the pre-release checks.
rc=0
fail() { echo "FAIL: $*"; rc=1; }

for f in LICENSE NOTICE README.md CHANGELOG.md ROADMAP.md CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md; do
  [[ -f "$f" ]] || fail "missing community file $f"
done
for f in .github/CODEOWNERS .github/dependabot.yml .github/PULL_REQUEST_TEMPLATE.md; do
  [[ -f "$f" ]] || fail "missing $f"
done

extra="$(git ls-files | grep -E '^\.[^/]+/' | grep -vE '^\.(github|githooks)/' || true)"
extra+="$(git ls-files '*.md' | grep -v / |
  grep -vE '^(README|CHANGELOG|CONTRIBUTING|CODE_OF_CONDUCT|SECURITY|ROADMAP)\.md$' || true)"
[[ -z "$extra" ]] || fail "unexpected files tracked: $extra"

re='^(feat|fix|perf|refactor|test|docs|build|ci|chore)(\([a-z0-9-]+\))?!?: [a-z0-9].{0,70}[^.]$'
while IFS= read -r sha; do
  body="$(git log -1 --format=%B "$sha")"
  if [[ "$(printf '%s\n' "$body" | wc -l | tr -d ' ')" != 1 ]]; then
    fail "commit $sha has a multi-line message"
  elif ! [[ "$body" =~ $re ]]; then
    fail "commit $sha message is not a Conventional Commit: $body"
  fi
done < <(git rev-list HEAD)

while IFS= read -r f; do
  head -3 "$f" | grep -q 'SPDX-License-Identifier: Apache-2.0' || fail "no SPDX header: $f"
done < <(git ls-files '*.go' '*.sh' '.githooks/*')

if git grep -n 'TODO' -- '*.go' '*.sh' | grep -v 'TODO(roadmap)' | grep -v release-check.sh; then
  fail "bare TODO found"
fi

grep -Eq '^## \[[0-9]+\.[0-9]+\.[0-9]+\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$' CHANGELOG.md ||
  fail "CHANGELOG.md has no dated release section (## [x.y.z] - YYYY-MM-DD)"
[[ -z "$(git status --porcelain)" ]] || echo "note: working tree is not clean"
exit "$rc"
