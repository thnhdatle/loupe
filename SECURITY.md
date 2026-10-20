# Security policy

## Supported versions

| Version | Supported |
|---|---|
| latest `0.x` release | yes |
| older releases | no |

## Reporting a vulnerability

Use GitHub private vulnerability reporting:
<https://github.com/thnhdatle/loupe/security/advisories/new>. Do not open a public
issue. We aim to acknowledge a report within 72 hours.

## Practices

- `gitleaks` runs on every commit in CI and in `make lint`.
- `govulncheck ./...` runs in CI; findings that affect called code fail the build.
- `trivy image` runs on built images: 0 CRITICAL and 0 HIGH with a fix available.
- Images are distroless, non-root (65532) and compatible with a read-only root
  filesystem.
- Dependency licenses are checked by `go-licenses` against an allowlist.
