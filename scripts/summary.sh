#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Runs a command and prints a one-line PASS/FAIL summary.
set -uo pipefail
name="${1:?usage: summary.sh <name> <command...>}"
shift
start=$SECONDS
if "$@"; then
  echo "PASS ${name} ($((SECONDS - start))s)"
else
  rc=$?
  echo "FAIL ${name} (exit ${rc}, $((SECONDS - start))s)" >&2
  exit "$rc"
fi
