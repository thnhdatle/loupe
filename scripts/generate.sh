#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Code generation: protobuf (buf) when configured, then go generate.
if [[ -d api/proto && -f buf.gen.yaml ]]; then
  buf generate
fi
go generate ./...
