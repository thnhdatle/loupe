#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Installs the pinned CLI tools that `make bootstrap`, `make ci` and
# `make release-dry` need on a Linux CI runner (amd64 or arm64).
set -euo pipefail

GOLANGCI_LINT_VERSION=2.14.0
HADOLINT_VERSION=2.15.1
SHELLCHECK_VERSION=0.11.0
GITLEAKS_VERSION=8.30.1
GORELEASER_VERSION=2.18.3
KUBECONFORM_VERSION=0.8.0
TRIVY_VERSION=0.76.0
SYFT_VERSION=1.54.1
GOVULNCHECK_VERSION=1.9.0

BIN="${HOME}/.local/bin"
mkdir -p "$BIN"
echo "$BIN" >>"${GITHUB_PATH:-/dev/null}"
export PATH="$BIN:$PATH"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

case "$(uname -m)" in
  x86_64) goarch=amd64 uarch=x86_64 trivyarch=64bit shellarch=x86_64 gitleaksarch=x64 ;;
  aarch64 | arm64) goarch=arm64 uarch=arm64 trivyarch=ARM64 shellarch=aarch64 gitleaksarch=arm64 ;;
  *) echo "ci-install-tools: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
gh=https://github.com

curl -sSfL "$gh/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${goarch}.tar.gz" |
  tar -xz -C "$tmp"
install "$tmp/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${goarch}/golangci-lint" "$BIN/"

curl -sSfL -o "$BIN/hadolint" "$gh/hadolint/hadolint/releases/download/v${HADOLINT_VERSION}/hadolint-Linux-${uarch}"
chmod +x "$BIN/hadolint"

curl -sSfL "$gh/koalaman/shellcheck/releases/download/v${SHELLCHECK_VERSION}/shellcheck-v${SHELLCHECK_VERSION}.linux.${shellarch}.tar.xz" |
  tar -xJ -C "$tmp"
install "$tmp/shellcheck-v${SHELLCHECK_VERSION}/shellcheck" "$BIN/"

curl -sSfL "$gh/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/gitleaks_${GITLEAKS_VERSION}_linux_${gitleaksarch}.tar.gz" |
  tar -xz -C "$BIN" gitleaks

curl -sSfL "$gh/goreleaser/goreleaser/releases/download/v${GORELEASER_VERSION}/goreleaser_Linux_${uarch}.tar.gz" |
  tar -xz -C "$BIN" goreleaser

curl -sSfL "$gh/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/kubeconform-linux-${goarch}.tar.gz" |
  tar -xz -C "$BIN" kubeconform

curl -sSfL "$gh/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-${trivyarch}.tar.gz" |
  tar -xz -C "$BIN" trivy

curl -sSfL "$gh/anchore/syft/releases/download/v${SYFT_VERSION}/syft_${SYFT_VERSION}_linux_${goarch}.tar.gz" |
  tar -xz -C "$BIN" syft

GOBIN="$BIN" go install "golang.org/x/vuln/cmd/govulncheck@v${GOVULNCHECK_VERSION}"

for t in golangci-lint hadolint shellcheck gitleaks goreleaser kubeconform trivy syft govulncheck; do
  command -v "$t" >/dev/null || { echo "ci-install-tools: $t missing after install" >&2; exit 1; }
done
