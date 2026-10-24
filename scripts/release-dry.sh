#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Runs a GoReleaser snapshot.
args=(release --snapshot --clean)
if ! command -v syft >/dev/null 2>&1; then
  echo "note: syft not installed; skipping sbom"
  args+=(--skip=sbom)
fi
goreleaser "${args[@]}"
