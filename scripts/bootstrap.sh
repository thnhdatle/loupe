#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Checks the required tools, downloads modules, pulls images and sets the hooks path.
GOLANGCI_LINT_VERSION="2.14.0"
missing=0

check() { # name required(1|0) [version-command]
  local name="$1" required="$2"
  if ! command -v "$name" >/dev/null 2>&1; then
    if [[ "$required" == 1 ]]; then
      echo "MISSING (required): $name"
      missing=1
    else
      echo "missing (optional): $name"
    fi
    return
  fi
  echo "found: $name"
}

for t in go git docker golangci-lint hadolint shellcheck gitleaks goreleaser; do check "$t" 1; done
for t in buf kubeconform trivy govulncheck syft node k6 jq; do check "$t" 0; done

if command -v golangci-lint >/dev/null 2>&1; then
  have="$(golangci-lint --version | sed -n 's/.*version \([0-9.]*\).*/\1/p')"
  if [[ "$have" != "$GOLANGCI_LINT_VERSION" ]]; then
    echo "WARNING: golangci-lint $have found, $GOLANGCI_LINT_VERSION pinned"
  fi
fi

if command -v go >/dev/null 2>&1; then
  want="$(sed -n 's/^toolchain go//p' go.mod)"
  have="$(go env GOVERSION | sed 's/^go//')"
  [[ -z "$want" || "$want" == "$have" ]] || echo "WARNING: go $have active, go.mod toolchain is $want"
fi

if ((missing)); then
  echo "bootstrap: install the missing tools (see CONTRIBUTING.md)" >&2
  exit 1
fi

go mod download
git config core.hooksPath .githooks

if docker info >/dev/null 2>&1; then
  docker compose -f deploy/docker-compose.yml pull --ignore-buildable \
    || echo "WARNING: could not pull compose images; set RAYLD_*_IMAGE to local images"
else
  echo "WARNING: docker daemon not reachable; skipped image pull"
fi
echo "hooksPath=$(git config core.hooksPath)"
