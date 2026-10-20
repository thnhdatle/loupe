# SPDX-License-Identifier: Apache-2.0
# Every target prints a one-line PASS/FAIL summary.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := ci

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# Release shown in the acceptance report heading.
RELEASE    ?= v0.1.0
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)
IMAGE      ?= thnhdatle/loupe:dev
COMPOSE    := docker compose -f deploy/docker-compose.yml
FUZZTIME   ?= 30s
export CGO_ENABLED := 0
SUMMARY    := ./scripts/summary.sh

.PHONY: bootstrap generate fmt lint test test-integration coverage mutation acceptance bench fuzz \
	examples quickstart-test security build docker up down ci release-check release-dry

bootstrap:
	@$(SUMMARY) bootstrap ./scripts/bootstrap.sh

generate:
	@$(SUMMARY) generate ./scripts/generate.sh

fmt:
	@$(SUMMARY) fmt go tool gofumpt -w .

lint:
	@$(SUMMARY) lint ./scripts/lint.sh

# The race detector needs cgo.
test: export CGO_ENABLED := 1
test:
	@$(SUMMARY) test go test -race -count=1 ./...

test-integration: export CGO_ENABLED := 1
test-integration:
	@$(SUMMARY) test-integration go test -race -tags integration -count=1 ./...

coverage: export CGO_ENABLED := 1
coverage:
	@$(SUMMARY) coverage ./scripts/coverage.sh

mutation:
	@$(SUMMARY) mutation ./scripts/mutation.sh

acceptance:
	@CI="$(CI)" ONLY="$(ONLY)" $(SUMMARY) acceptance go run ./acceptance/cmd/runner -version "$(RELEASE)"

bench:
	@$(SUMMARY) bench ./scripts/bench.sh

fuzz:
	@FUZZTIME=$(FUZZTIME) $(SUMMARY) fuzz ./scripts/fuzz.sh

examples:
	@$(SUMMARY) examples ./scripts/examples.sh

quickstart-test:
	@$(SUMMARY) quickstart-test ./scripts/quickstart-test.sh

security:
	@RAYLD_LOUPE_IMAGE=$(IMAGE) $(SUMMARY) security ./scripts/security.sh

build:
	@mkdir -p bin
	@$(SUMMARY) build go build -trimpath -ldflags "$(LDFLAGS)" -o bin/loupe ./cmd/loupe

docker:
	@$(SUMMARY) docker docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) -t $(IMAGE) .

up:
	@RAYLD_LOUPE_IMAGE=$(IMAGE) $(SUMMARY) up $(COMPOSE) up -d --build --wait

down:
	@$(SUMMARY) down $(COMPOSE) down -v

ci:
	@$(SUMMARY) ci $(MAKE) --no-print-directory lint test test-integration coverage security

release-check:
	@$(SUMMARY) release-check ./scripts/release-check.sh

release-dry:
	@$(SUMMARY) release-dry ./scripts/release-dry.sh
