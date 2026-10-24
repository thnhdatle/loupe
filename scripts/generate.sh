#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Runs code generation.
if [[ -d api/proto && -f buf.gen.yaml ]]; then
  buf generate
fi
go generate ./...
