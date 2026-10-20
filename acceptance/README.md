# Acceptance suite

Run the end-to-end tests behind Loupe's published numbers and read their results.
The latest results are in [`report.md`](report.md); benchmark numbers are in
[`bench/RESULTS.md`](../bench/RESULTS.md).

## Running

From the repository root:

```sh
make acceptance                          # every criterion, reference-machine thresholds
make acceptance CI=1                     # CI thresholds
make acceptance ONLY=LPE-01,LPE-04       # a subset
go run ./acceptance/cmd/runner --check-only   # only check criteria.yaml against the tests
```

It starts [`deploy/docker-compose.yml`](../deploy/docker-compose.yml), writes `acceptance/report.md`,
and exits non-zero on any FAIL.

| Runner flag | Effect |
|---|---|
| `--no-build` | skip the build and `make docker` |
| `--no-compose` | use a stack that is already running |
| `--check-only` | only check criteria against tests |
| `-version` | release in the report heading (`make acceptance` passes `RELEASE`) |

## Requirements

- Go (version from `go.mod`).
- Docker with Compose v2 (reference: 8 CPUs, 10 GB for the Docker VM).
- For LPE-01 and LPE-12: the fakellm image (`ghcr.io/thnhdatle/fakellm:v0.1.0`, a local
  `thnhdatle/fakellm:dev`, or any image named by `RAYLD_FAKELLM_IMAGE`).
- For LPE-01: `python3.12` and [uv](https://docs.astral.sh/uv/).
- For LPE-01 and LPE-11: Node.js 20 or later, plus in `tests/node`:

  ```sh
  npm ci                                       # install the pinned packages
  npx playwright install --with-deps chromium  # browser for the dashboard tests
  ```

- For reference-machine thresholds: a MacBook Pro M3 Pro with 36 GB (`uname -sm` is `Darwin arm64`).
- At least 30 GB of free disk. LPE-04 caches its database in `~/.cache/rayld/loupe/lpe04`.
- About 1.5 to 2 hours for a full run.

| Variable (optional) | Effect |
|---|---|
| `CI=1` | CI thresholds |
| `RAYLD_FAKELLM_IMAGE` | override the fakellm image |
| `LOUPE_LPE07_SEED` | replay a previous LPE-07 kill schedule |
| `XDG_CACHE_HOME` | move the LPE-04 dataset cache |

## Criteria

Thresholds are in [`criteria.yaml`](criteria.yaml): pass.mac for the reference machine, pass.ci for CI.
Each id needs exactly one test named after it (for example TestAcceptance_LPE01_…), or the run fails.

| ID | Kind | What it checks |
|---|---|---|
| LPE-01 | functional | Real SDKs (OTel GenAI Python and Node, OpenInference Python, manual Go) produce the expected traces |
| LPE-02 | performance | 3,000 spans/s for 10 minutes, 3 runs: nothing lost |
| LPE-03 | performance | Overload at 3 × capacity: bounded memory, every span accounted for |
| LPE-04 | performance | p95 query latency on a 2M-span, 30-day database |
| LPE-05 | functional | 200 hand-checked cost cases match exactly |
| LPE-06 | performance | Startup time, idle memory, image size |
| LPE-07 | functional | 50 random kill -9 runs: restart succeeds, no loss beyond the queue (none with sync_ack) |
| LPE-08 | functional | 30 simulated days: expired data deleted, disk cap holds |
| LPE-09 | functional | Rollup percentiles within 2% of exact |
| LPE-10 | functional | Capture off: no content stored or logged |
| LPE-11 | functional | Dashboard smoke test, no console errors |
| LPE-12 | release | README quickstart works, trace visible within 10 s |

Off the reference machine and without `CI=1`, performance criteria are reported as Blocked.

## The report

`report.md` has one row per criterion: result, measured, threshold. Logs go to
`acceptance/logs/<ID>.txt` and `.json` (git-ignored).

| Result | Meaning |
|---|---|
| **PASS** | every value meets its threshold |
| **FAIL** | a value misses its threshold; also listed under "Not met" |
| **Skipped (environment)** | an optional capability is missing (for example the fakellm image) |
| **Blocked (environment)** | a required capability is missing, or reference thresholds cannot apply on this host |
