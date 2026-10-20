# Acceptance suite

End-to-end tests that check Loupe's guarantees against the real binary, real
OpenTelemetry SDKs and a real browser: SDK compatibility, sustained ingest,
overload behaviour, query latency, cost correctness, footprint, crash safety,
retention, percentile accuracy, privacy, the dashboard and the README
quickstart. The numbers in the README and in
[`bench/RESULTS.md`](../bench/RESULTS.md) come from these tests; the latest
results are in [`report.md`](report.md).

## Running

From the repository root:

```sh
make acceptance                          # every criterion, reference-machine thresholds
make acceptance CI=1                     # CI thresholds
make acceptance ONLY=LPE-01,LPE-04       # a subset
go run ./acceptance/cmd/runner --check-only   # only check criteria.yaml against the tests
```

The runner (`acceptance/cmd/runner`) builds the project and the image
(`make docker`), starts the stack in
[`deploy/docker-compose.yml`](../deploy/docker-compose.yml) (Loupe and the
fakellm provider), checks the requirements of each criterion, runs each
criterion test with `go test -tags acceptance -count=1`, writes
`acceptance/report.md`, and exits non-zero on any FAIL. Runner flags:
`--no-build` (skip the build and `make docker`), `--no-compose` (use a stack
that is already running), `--check-only`, `-version` (the release shown in the
report heading; `make acceptance` passes `RELEASE` from the Makefile).

Requirements:

- Go (version from `go.mod`);
- Docker with Compose v2; the reference machine runs the Docker VM with 8 CPUs
  and 10 GB of memory;
- the fakellm image (`ghcr.io/thnhdatle/fakellm:v0.1.0`, or a local
  `thnhdatle/fakellm:dev`, or any image named by `RAYLD_FAKELLM_IMAGE`) for
  LPE-01 and LPE-12;
- `python3.12` and [uv](https://docs.astral.sh/uv/) for the Python SDK runs of
  LPE-01;
- Node.js 20 or later with `npm ci` run in `tests/node`, and the Playwright
  browsers (`npx playwright install --with-deps chromium` in `tests/node`) for
  LPE-01 and LPE-11;
- macOS on Apple silicon (`uname -sm` is `Darwin arm64`) for the
  reference-machine thresholds of the performance criteria; the reference
  machine is a MacBook Pro M3 Pro with 36 GB (`machine` in `criteria.yaml`);
- at least 30 GB of free disk: the ingest and overload runs create databases
  of several GB each (deleted after each run), and LPE-04 keeps its generated
  2M-span database under `~/.cache/rayld/loupe/lpe04` between runs;
- about 1.5 to 2 hours for a full run on the reference machine (LPE-02 alone
  runs 3 × 10 minutes plus a 10-minute run at 5,000 spans/s; LPE-06 idles for
  10 minutes).

Environment variables (optional): `CI=1` selects the CI thresholds;
`RAYLD_FAKELLM_IMAGE` overrides the fakellm image; `LOUPE_LPE07_SEED` replays
the random kill schedule of a previous LPE-07 run; `XDG_CACHE_HOME` moves the
LPE-04 dataset cache.

## Criteria

Each criterion has an id, a test named after it (`TestAcceptance_LPE01_…` or
`BenchmarkAcceptance_…`), a kind, the capabilities it requires, and thresholds
for two environments in [`criteria.yaml`](criteria.yaml): `pass.mac` for the
reference machine and `pass.ci` for CI runners. Tests read the thresholds at
run time; no threshold is written in a test.

| ID | Kind | What it checks |
|---|---|---|
| LPE-01 | functional | Real SDKs (OTel GenAI Python and Node, OpenInference Python, manual Go spans) against fakellm produce the expected trace tree, kinds, models, tokens, cost and content |
| LPE-02 | performance | Sustained OTLP/HTTP ingest at a constant 3,000 spans/s for 10 minutes, 3 runs: nothing rejected, dropped or lost |
| LPE-03 | performance | Overload at 3 × the measured capacity in both overflow modes: bounded memory, every span accounted for, gRPC rejections carry RetryInfo |
| LPE-04 | performance | p95 latency of the 8 benchmark dashboard and API queries on a 2M-span, 30-day database |
| LPE-05 | functional | Cost of 200 hand-checked cases (`testdata/cost/cases.yaml`) matches exactly |
| LPE-06 | performance | Footprint: start-to-ready time, idle memory and image size of the static binary |
| LPE-07 | functional | Crash safety: 50 runs of `kill -9` at random points under load, including during retention and price recompute; integrity check and restart succeed, no loss beyond the queue (none with `sync_ack`) |
| LPE-08 | functional | Retention over 30 simulated days: expired spans and content are deleted, the disk cap holds, rollups stay, commit latency stays bounded |
| LPE-09 | functional | Rollup percentiles (p50, p95, p99) stay within 2% of the exact values for 5 latency distributions |
| LPE-10 | functional | With capture off, no content marker reaches the database, its files or the logs |
| LPE-11 | functional | Dashboard smoke test with Playwright: login flow, all pages, nested agent tree and content hints, no console errors |
| LPE-12 | release | The README quickstart works from a clean checkout and a trace is visible within 10 s |

Performance thresholds are calibrated for the reference machine. On any
other host without `CI=1`, those criteria are reported as Blocked.

## The report

`report.md` has one row per criterion with the result, the measured values
and the thresholds, followed by the sibling images used. Per-criterion logs
(`acceptance/logs/<ID>.txt` and `.json`) are written locally during the run
and are not committed. Results are:

- **PASS**: every measured value meets its threshold;
- **FAIL**: a measured value misses its threshold; listed again under
  "Not met";
- **Skipped (environment)**: an optional capability is missing on this host
  (for example the fakellm image);
- **Blocked (environment)**: a required capability is missing or the
  reference-machine thresholds cannot apply on this host (for example a
  performance criterion off the reference machine).

The runner, not the test, decides when a criterion is skipped or blocked, so
the acceptance sources contain no skip constructs. Before running, it also
checks that every criterion has exactly one test with its id and that the test
name matches `criteria.yaml`; any mismatch fails the run.
