# Contributing to Loupe

Thanks for helping. For larger changes, open an issue first and read the
[architecture section of the README](README.md#architecture).

## Prerequisites

- Go (version from `go.mod`) and a C compiler: `make test` runs the race
  detector, which needs cgo.
- For `make lint`: golangci-lint 2.14.0, hadolint, shellcheck, gitleaks and
  kubeconform; `make bootstrap` also expects goreleaser, and `make security`
  needs govulncheck and trivy. `scripts/ci-install-tools.sh` lists the pinned
  versions and installs them on Linux.
- Node >= 20 locally (CI pins Node LTS 24), Playwright (`@playwright/test`
  1.64.x), uv and Docker are needed only for `make acceptance` and
  `make examples`.

Run `make bootstrap` once; it checks that the required tools are installed,
downloads the Go modules, pulls the compose images and sets the git hooks path.

### Linux notes

- Install Go from <https://go.dev/dl/>, then the tools above (on a Linux
  machine `scripts/ci-install-tools.sh` installs them into `~/.local/bin`,
  which must be on `PATH`), then run `make bootstrap`.
- Install Docker Engine and add your user to the `docker` group.
- Install Node via your package manager or nvm, uv via
  `curl -LsSf https://astral.sh/uv/install.sh | sh`.
- Playwright browsers need system libraries: `npx playwright install --with-deps`.
- Acceptance needs at least 30 GB free disk ([acceptance/README.md](acceptance/README.md)).

## Makefile targets

`bootstrap, generate, fmt, lint, test, test-integration, coverage, mutation,
acceptance, bench, fuzz, examples, quickstart-test, security, build, docker, up,
down, ci, release-check, release-dry`. Every target prints a one-line PASS/FAIL.

| Level | Command |
|---|---|
| Unit (race, goleak) | `make test` |
| Integration | `make test-integration` |
| Coverage threshold | `make coverage` |
| Mutation | `make mutation` |
| Fuzz | `make fuzz FUZZTIME=30s` |
| Acceptance | `make acceptance` (`ONLY=LPE-01,LPE-04` for a subset) |
| Everything CI runs first | `make ci` |

## Commit format

One line only, no body or trailers: `<type>(<scope>): <summary>`.

- `type`: feat, fix, perf, refactor, test, docs, build, ci, chore.
- `scope`: a package or area, for example `otlp`, `writer`, `sqlite`, `api`,
  `web`, `bench`, `deps`.
- Breaking change: `feat(api)!: ...`.
- Summary: imperative, lowercase, no trailing period, whole line <= 72 chars.

Example: `perf(sqlite): batch inserts in a single transaction`.

## Adding a dependency

Record this checklist in the PR:

- [ ] Maintained within the last 12 months
- [ ] License in {Apache-2.0, MIT, MIT-CMU, BSD-2/3, ISC, MPL-2.0, 0BSD, Zlib,
  PSF-2.0, Unlicense, CC0-1.0}
- [ ] No transitive GPL/AGPL (`go-licenses check ./...`)
- [ ] Vendored front-end assets are listed in `NOTICE`

## Proposing a design change

Open an issue first for anything that changes the public API, the storage
schema or runtime behaviour, and describe the decision and the alternatives
considered in the issue or the PR.

## Pull request checklist

Tests, docs, `CHANGELOG.md` entry under `[Unreleased]`; `make ci`
passes.

## Questions

Open a GitHub Discussion or issue.
