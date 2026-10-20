# syntax=docker/dockerfile:1
# check=skip=SecretsUsedInArgOrEnv;error=true
# (the flagged variables are a token file path and a boolean, not secret values)
# SPDX-License-Identifier: Apache-2.0

# BIN_SOURCE selects where the binary comes from: "build" compiles from source
# (make docker); "prebuilt" copies the binary GoReleaser put in the build context.
ARG BIN_SOURCE=build

FROM --platform=$BUILDPLATFORM golang:1.27@sha256:f24164268fc1e741bbdc0066dbadcf1c8426c5a8489f0729c02c3905a53032fd AS compile
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
      -o /out/loupe ./cmd/loupe

FROM scratch AS build
COPY --from=compile /out/loupe /loupe

FROM scratch AS prebuilt
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/loupe /loupe

FROM ${BIN_SOURCE} AS bin

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=bin /loupe /usr/local/bin/loupe
# LOUPE_API_TOKEN_FILE is a path to a token file, not a secret value.
# hadolint ignore=DL3064
ENV LOUPE_STORAGE_PATH=/data/loupe.db \
    LOUPE_RECEIVER_GRPC_LISTEN=0.0.0.0:4317 \
    LOUPE_RECEIVER_HTTP_LISTEN=0.0.0.0:4318 \
    LOUPE_UI_LISTEN=0.0.0.0:3000 \
    LOUPE_RECEIVER_ALLOW_UNAUTHENTICATED=true \
    LOUPE_API_TOKEN_FILE=/data/api_token \
    LOUPE_API_GENERATE_TOKEN=true
USER 65532:65532
# WORKDIR creates /data owned by the non-root user, so a fresh volume is writable.
WORKDIR /data
VOLUME /data
EXPOSE 3000 4317 4318 9092
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/loupe", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/loupe"]
CMD ["serve"]
