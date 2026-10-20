#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# All linters run; the script fails if any of them reports a problem.
rc=0
step() { echo "== $*"; }

step gofumpt
out="$(go tool gofumpt -l . )"
if [[ -n "$out" ]]; then echo "unformatted files:"; echo "$out"; rc=1; fi

step "go mod tidy"
go mod tidy -diff || rc=1

step golangci-lint
golangci-lint run || rc=1

step hadolint
hadolint Dockerfile || rc=1

step shellcheck
shopt -s nullglob
shellcheck scripts/*.sh .githooks/* || rc=1

step gitleaks
gitleaks detect --source . --redact --no-banner || rc=1

if [[ -d deploy/k8s ]]; then
  step kubeconform
  kubeconform -strict -summary -ignore-filename-pattern kustomization deploy/k8s || rc=1
fi

if [[ -d api/proto ]]; then
  step buf
  buf lint || rc=1
fi

exit "$rc"
