# automatically set by buildkit, can be changed with --platform flag
# see https://docs.docker.com/engine/reference/builder/#automatic-platform-args-in-the-global-scope
# TARGETOS
# TARGETARCH
# TARGETPLATFORM
# BUILDPLATFORM

# All target images use this as base image, and add the final build results.
# It will default to the target platform.
ARG TARGET_BASE_IMAGE=alpine:3.20

# The ubuntu target base image is used for the op-challenger build with kona and asterisc.
ARG UBUNTU_TARGET_BASE_IMAGE=ubuntu:22.04

# The version of kona to use.
# The only build that uses this is `op-challenger-target`.
ARG KONA_VERSION=none

# The version of asterisc to use.
# The only build that uses this is `op-challenger-target`.
ARG ASTERISC_VERSION=none

# We may be cross-building for another platform. Specify which platform we need as builder.
FROM --platform=$BUILDPLATFORM golang:1.22.7-alpine3.20 AS builder

RUN apk add --no-cache curl tar gzip make gcc musl-dev linux-headers git jq bash

# Install mise (the apk version is outdated)
RUN curl https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh

ARG TARGETARCH

# Install yq
RUN wget https://github.com/mikefarah/yq/releases/latest/download/yq_linux_$TARGETARCH -O /usr/local/bin/yq && \
    chmod +x /usr/local/bin/yq

# Install versioned toolchain
COPY ./mise.toml .
RUN mise trust && mise install -v -y just && cp $(mise which just) /usr/local/bin/just && just --version

# We copy the go.mod/sum first, so the `go mod download` does not have to re-run if dependencies do not change.
COPY ./go.mod /app/go.mod
COPY ./go.sum /app/go.sum

WORKDIR /app

RUN echo "go mod cache: $(go env GOMODCACHE)"
RUN echo "go build cache: $(go env GOCACHE)"

# warm-up the cache
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go mod download

# NOTE: the Dockerfile.dockerignore file effectively describes all dependencies
COPY . /app

# We avoid copying the full .git dir into the build for just some metadata.
# Instead, specify:
# --build-arg GIT_COMMIT=$(git rev-parse HEAD)
# --build-arg GIT_DATE=$(git show -s --format='%ct')
ARG GIT_COMMIT
ARG GIT_DATE

ARG TARGETOS

# separate docker-builds:
# - proxyd
# - any JS/TS/smart-contract builds

# Build the Go services, utilizing caches and share the many common packages.
# The "id" defaults to the value of "target", the cache will thus be reused during this build.
# "sharing" defaults to "shared", the cache will thus be available to other concurrent docker builds.

FROM --platform=$BUILDPLATFORM us-docker.pkg.dev/oplabs-tools-artifacts/images/cannon:v1.0.0 AS cannon-builder-v1-0-0
FROM --platform=$BUILDPLATFORM us-docker.pkg.dev/oplabs-tools-artifacts/images/cannon:v1.2.0 AS cannon-builder-v1-2-0
FROM --platform=$BUILDPLATFORM us-docker.pkg.dev/oplabs-tools-artifacts/images/cannon:v1.3.0 AS cannon-builder-v1-3-0

FROM --platform=$BUILDPLATFORM builder AS cannon-builder
ARG CANNON_VERSION=v0.0.0
# Copy cannon binaries from previous versions
COPY --from=cannon-builder-v1-0-0 /usr/local/bin/cannon ./cannon/multicannon/embeds/cannon-0
COPY --from=cannon-builder-v1-2-0 /usr/local/bin/cannon-3 ./cannon/multicannon/embeds/cannon-3
COPY --from=cannon-builder-v1-3-0 /usr/local/bin/cannon-1 ./cannon/multicannon/embeds/cannon-1
COPY --from=cannon-builder-v1-3-0 /usr/local/bin/cannon-4 ./cannon/multicannon/embeds/cannon-4
# Build current binaries
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd cannon && make cannon  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE VERSION="$CANNON_VERSION"

FROM --platform=$BUILDPLATFORM builder AS cp-program-builder
ARG OP_PROGRAM_VERSION=v0.0.0
# note: we only build the host, that's all the user needs. No Go MIPS cross-build in docker
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd cp-program && make cp-program-host  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE VERSION="$OP_PROGRAM_VERSION"

FROM --platform=$BUILDPLATFORM builder AS op-wheel-builder
ARG OP_WHEEL_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-wheel && make op-wheel  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE  VERSION="$OP_WHEEL_VERSION"

FROM --platform=$BUILDPLATFORM builder AS cp-node-builder
ARG OP_NODE_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd cp-node && make cp-node  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE VERSION="$OP_NODE_VERSION"

FROM --platform=$BUILDPLATFORM builder AS op-challenger-builder
ARG OP_CHALLENGER_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-challenger && make op-challenger  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE  VERSION="$OP_CHALLENGER_VERSION"

FROM --platform=$BUILDPLATFORM builder AS op-dispute-mon-builder
ARG OP_DISPUTE_MON_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-dispute-mon && make op-dispute-mon  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE  VERSION="$OP_DISPUTE_MON_VERSION"

FROM --platform=$BUILDPLATFORM builder AS op-batcher-builder
ARG OP_BATCHER_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-batcher && make op-batcher  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE  VERSION="$OP_BATCHER_VERSION"

FROM --platform=$BUILDPLATFORM builder AS op-proposer-builder
ARG OP_PROPOSER_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-proposer && make op-proposer  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE  VERSION="$OP_PROPOSER_VERSION"

FROM --platform=$BUILDPLATFORM builder AS op-conductor-builder
ARG OP_CONDUCTOR_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-conductor && make op-conductor  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE  VERSION="$OP_CONDUCTOR_VERSION"

FROM --platform=$BUILDPLATFORM builder AS da-server-builder
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-alt-da && make da-server  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE

FROM --platform=$BUILDPLATFORM builder AS cp-supervisor-builder
ARG OP_SUPERVISOR_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd cp-supervisor && make cp-supervisor  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE  VERSION="$OP_SUPERVISOR_VERSION"

FROM --platform=$BUILDPLATFORM builder AS cp-deployer-builder
ARG OP_NODE_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-chain-ops && make cp-deployer  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE VERSION="$OP_DEPLOYER_VERSION"

FROM --platform=$BUILDPLATFORM builder AS op-dripper-builder
ARG OP_DRIPPER_VERSION=v0.0.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build cd op-dripper && make op-dripper  \
  GOOS=$TARGETOS GOARCH=$TARGETARCH GITCOMMIT=$GIT_COMMIT GITDATE=$GIT_DATE VERSION="$OP_DRIPPER_VERSION"

FROM $TARGET_BASE_IMAGE AS cannon-target
COPY --from=cannon-builder /app/cannon/bin/cannon /usr/local/bin/
COPY --from=cannon-builder /app/cannon/multicannon/embeds/* /usr/local/bin/
CMD ["cannon"]

FROM $TARGET_BASE_IMAGE AS cp-program-target
COPY --from=cp-program-builder /app/cp-program/bin/cp-program /usr/local/bin/
CMD ["cp-program"]

FROM $TARGET_BASE_IMAGE AS op-wheel-target
COPY --from=op-wheel-builder /app/op-wheel/bin/op-wheel /usr/local/bin/
CMD ["op-wheel"]

FROM $TARGET_BASE_IMAGE AS cp-node-target
COPY --from=cp-node-builder /app/cp-node/bin/cp-node /usr/local/bin/
CMD ["cp-node"]

# Make the kona docker image published by upstream available as a source to copy kona from.
FROM ghcr.io/op-rs/kona/kona-host:$KONA_VERSION AS kona
# Make the asterisc docker image published by upstream available as a source to copy asterisc from.
FROM us-docker.pkg.dev/oplabs-tools-artifacts/images/asterisc:$ASTERISC_VERSION AS asterisc

# Also produce an op-challenger loaded with kona and asterisc using ubuntu
FROM $UBUNTU_TARGET_BASE_IMAGE AS op-challenger-target
RUN apt-get update && apt-get install -y --no-install-recommends musl openssl ca-certificates
COPY --from=op-challenger-builder /app/op-challenger/bin/op-challenger /usr/local/bin/
# Copy in cp-program and cannon
COPY --from=cp-program-builder /app/cp-program/bin/cp-program /usr/local/bin/
ENV OP_CHALLENGER_CANNON_SERVER=/usr/local/bin/cp-program
COPY --from=cannon-builder /app/cannon/bin/cannon /usr/local/bin/
ENV OP_CHALLENGER_CANNON_BIN=/usr/local/bin/cannon
# Copy in kona and asterisc
COPY --from=kona /kona-host /usr/local/bin/
ENV OP_CHALLENGER_ASTERISC_KONA_SERVER=/usr/local/bin/kona-host
COPY --from=asterisc /usr/local/bin/asterisc /usr/local/bin/
ENV OP_CHALLENGER_ASTERISC_BIN=/usr/local/bin/asterisc
CMD ["op-challenger"]

FROM $TARGET_BASE_IMAGE AS op-dispute-mon-target
COPY --from=op-dispute-mon-builder /app/op-dispute-mon/bin/op-dispute-mon /usr/local/bin/
CMD ["op-dispute-mon"]

FROM $TARGET_BASE_IMAGE AS op-batcher-target
COPY --from=op-batcher-builder /app/op-batcher/bin/op-batcher /usr/local/bin/
CMD ["op-batcher"]

FROM $TARGET_BASE_IMAGE AS op-proposer-target
COPY --from=op-proposer-builder /app/op-proposer/bin/op-proposer /usr/local/bin/
CMD ["op-proposer"]

FROM $TARGET_BASE_IMAGE AS op-conductor-target
COPY --from=op-conductor-builder /app/op-conductor/bin/op-conductor /usr/local/bin/
CMD ["op-conductor"]

FROM $TARGET_BASE_IMAGE AS da-server-target
COPY --from=da-server-builder /app/op-alt-da/bin/da-server /usr/local/bin/
CMD ["da-server"]

FROM $TARGET_BASE_IMAGE AS cp-supervisor-target
COPY --from=cp-supervisor-builder /app/cp-supervisor/bin/cp-supervisor /usr/local/bin/
CMD ["cp-supervisor"]

FROM $TARGET_BASE_IMAGE AS cp-deployer-target
COPY --from=cp-deployer-builder /app/op-chain-ops/bin/cp-deployer /usr/local/bin/
CMD ["cp-deployer"]

FROM $TARGET_BASE_IMAGE AS op-dripper-target
COPY --from=op-dripper-builder /app/op-dripper/bin/op-dripper /usr/local/bin/
CMD ["op-dripper"]
