#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# govulncheck, trivy on the local image, go-licenses against the allowlist.
IMAGE="${RAYLD_LOUPE_IMAGE:-thnhdatle/loupe:dev}"
ALLOWED="Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC,MPL-2.0,0BSD"
rc=0
echo "== govulncheck"
govulncheck ./... || rc=1
echo "== go-licenses"
# modernc.org/mathutil (dependency of modernc.org/sqlite) ships a
# BSD-3-Clause LICENSE whose wording ("the names of the authors") the
# go-licenses classifier does not recognise; reviewed by hand, listed in NOTICE.
go tool go-licenses check ./... --allowed_licenses="$ALLOWED" --ignore modernc.org/mathutil || rc=1
if docker image inspect "$IMAGE" >/dev/null 2>&1; then
  echo "== trivy $IMAGE"
  trivy image --exit-code 1 --severity HIGH,CRITICAL --quiet "$IMAGE" || rc=1
else
  echo "WARNING: image $IMAGE not found; run make docker first (trivy skipped)"
  rc=1
fi
exit "$rc"
