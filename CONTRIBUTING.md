# Contributing to Loupe

Set up, test and submit a change to Loupe. For larger changes, open an issue first and read the
[architecture section of the README](README.md#architecture).

## Prerequisites

- Go (version from `go.mod`) and a C compiler. The race detector needs cgo.
- For `make lint`: golangci-lint 2.14.0, hadolint, shellcheck, gitleaks, kubeconform.
- For `make bootstrap`: also goreleaser.
- For `make security`: govulncheck, trivy.
- For `make examples` only: Node >= 20 (CI pins Node LTS 24), uv, Docker.

`scripts/ci-install-tools.sh` lists the pinned versions and installs them on Linux.

```sh
make bootstrap   # check tools, download Go modules, pull compose images, set git hooks path
```

### Linux notes

```sh
scripts/ci-install-tools.sh                         # installs into ~/.local/bin (must be on PATH)
curl -LsSf https://astral.sh/uv/install.sh | sh     # uv
```

- Install Go from <https://go.dev/dl/> first, then the tools, then run `make bootstrap`.
- Install Docker Engine and add your user to the docker group.
- Install Node with your package manager or nvm.

## Makefile targets

`bootstrap, generate, fmt, lint, test, test-integration, coverage, fuzz, examples,
quickstart-test, security, build, docker, up, down, ci, release-check, release-dry`. Each prints a one-line PASS/FAIL.

| Level | Command |
|---|---|
| Unit (race, goleak) | `make test` |
| Integration | `make test-integration` |
| Coverage threshold | `make coverage` |
| Fuzz | `make fuzz FUZZTIME=30s` |
| Everything CI runs first | `make ci` |

## Commit format

One line only, no body or trailers: `<type>(<scope>): <summary>`. The commit-msg hook rejects
anything else.

- Type: feat, fix, perf, refactor, test, docs, build, ci, chore.
- Scope: a package or area, for example otlp, writer, sqlite, api, web, deps.
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

Open an issue first for changes to the public API, the storage schema or runtime behaviour.
Describe the decision and the alternatives.

## Pull request checklist

Tests, docs, a `CHANGELOG.md` entry under `[Unreleased]`, and `make ci` passes.

## Questions

Open a GitHub Discussion or issue.
