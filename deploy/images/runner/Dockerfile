# syntax=docker/dockerfile:1
# Agent pod image: the runner plus Claude Code (docs/spec/04, docs/spec/05).
# This is the M1 subset of factory-base; toolchains per repo come with M2.
# Build from the repo root: docker build -f deploy/images/runner/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/runner ./cmd/runner

FROM debian:trixie-slim
# Pinned to the version the adapter was verified against (mise run test:smoke-claude).
ARG CLAUDE_CODE_VERSION=2.1.323
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 10001 --user-group --shell /bin/bash agent \
 && install -d -o 10001 -g 10001 /workspace
USER 10001:10001
# The native installer verifies the binary's checksum before installing it.
RUN curl -fsSL https://claude.ai/install.sh | bash -s "${CLAUDE_CODE_VERSION}" \
 && /home/agent/.local/bin/claude --version | grep -q "^${CLAUDE_CODE_VERSION} "
ENV PATH=/home/agent/.local/bin:$PATH \
    DISABLE_AUTOUPDATER=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
COPY --from=build /out/runner /usr/local/bin/runner
WORKDIR /workspace
EXPOSE 7070
ENTRYPOINT ["/usr/local/bin/runner", "--listen", ":7070", "--workdir", "/workspace", "--claude-bin", "/home/agent/.local/bin/claude"]
