#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Prints the CHANGELOG.md section of a release: release-notes.sh v0.1.0 (or 0.1.0).
# Fails when the section is missing or empty.
ver="${1:?usage: release-notes.sh <tag>}"
ver="${ver#v}"
notes="$(awk -v v="$ver" '
  $0 ~ "^## \\[" v "\\]" { on = 1; next }
  on && /^## \[/ { exit }
  on && /^\[[^]]+\]: / { exit }
  on
' CHANGELOG.md)"
if [[ -z "$(tr -d '[:space:]' <<<"$notes")" ]]; then
  echo "release-notes: no CHANGELOG.md section for ${ver}" >&2
  exit 1
fi
printf '%s\n' "$notes"
