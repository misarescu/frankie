# syntax=docker/dockerfile:1
# Control plane (docs/spec/01). Build from the repo root:
#   docker build -f deploy/images/controlplane/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/controlplane ./cmd/controlplane

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/controlplane /controlplane
# Numeric (distroless "nonroot") so runAsNonRoot can be verified by the kubelet.
USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/controlplane"]
