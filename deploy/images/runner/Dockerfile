# syntax=docker/dockerfile:1
# Agent pod image: the runner plus Claude Code, opencode, pi and mise (docs/spec/04, docs/spec/05).
# This is the platform base image (factory-base, D30): toolchain images layer a
# repo's mise.toml on top of it, so a new build of this image rebuilds them all.
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
ARG TARGETARCH
# Pinned to the version the adapter was verified against (mise run test:smoke-claude).
ARG CLAUDE_CODE_VERSION=2.1.323
# Pinned to the version the opencode adapter's tests run (mise.toml); the
# checksums are the release assets' (mise.lock). Downloaded from GitHub, not
# opencode.ai, which also serves opencode's own models and is never allowlisted.
ARG OPENCODE_VERSION=2.2.7
ARG OPENCODE_SHA256_AMD64=89a6cee227b54bc011179c528a5e63176032462b1a5e21723b616801cddc2ca7
ARG OPENCODE_SHA256_ARM64=c1682057f0815cb2abdd6e00305f1146ebaf1c167f0335cdb74ddc80008120e7
# Likewise for pi (the release's SHA256SUMS). Its binary loads assets from
# its own directory, so the whole release goes to /opt/pi.
ARG PI_VERSION=1.5.2
ARG PI_SHA256_AMD64=811c6f2c9e7aaee252b5404252e556fb713aa209c1f7f924226aa77d4ef70cb5
ARG PI_SHA256_ARM64=42805cd5615952fcbbfc9c56b04f8feb84395134952e169a6cdc8d7676975666
# mise runs the repo's tasks and installs its pinned tools (D15). The installer
# verifies the release checksum.
ARG MISE_VERSION=v2026.11.7
# gcc and libc6-dev give Go a C toolchain for cgo, which `go test -race` needs
# (D34); mise does not provide one.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gcc git jq libc6-dev make ripgrep \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL https://mise.run | MISE_VERSION="${MISE_VERSION}" MISE_INSTALL_PATH=/usr/local/bin/mise sh \
 && mise --version | grep -q "${MISE_VERSION#v}" \
 && case "${TARGETARCH}" in \
      amd64) oc_arch=x64; oc_sum="${OPENCODE_SHA256_AMD64}"; pi_sum="${PI_SHA256_AMD64}" ;; \
      arm64) oc_arch=arm64; oc_sum="${OPENCODE_SHA256_ARM64}"; pi_sum="${PI_SHA256_ARM64}" ;; \
      *) echo "unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && curl -fsSL -o /tmp/opencode.tar.gz "https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${oc_arch}.tar.gz" \
 && echo "${oc_sum}  /tmp/opencode.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin opencode \
 && rm /tmp/opencode.tar.gz \
 && opencode --version | grep -q "v${OPENCODE_VERSION}$" \
 && curl -fsSL -o /tmp/pi.tar.gz "https://github.com/earendil-works/pi/releases/download/v${PI_VERSION}/pi-linux-${oc_arch}.tar.gz" \
 && echo "${pi_sum}  /tmp/pi.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/pi.tar.gz -C /opt \
 && rm /tmp/pi.tar.gz \
 && ln -s /opt/pi/pi /usr/local/bin/pi \
 && /opt/pi/pi --version | grep -qx "${PI_VERSION}" \
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
ENTRYPOINT ["/usr/local/bin/runner", "--listen", ":7070", "--workdir", "/workspace", "--claude-bin", "/home/agent/.local/bin/claude", "--opencode-bin", "/usr/local/bin/opencode", "--pi-bin", "/opt/pi/pi"]
