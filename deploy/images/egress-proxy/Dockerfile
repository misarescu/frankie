# syntax=docker/dockerfile:1
# Egress proxy for agent pods (D20). Build from the repo root:
#   docker build -f deploy/images/egress-proxy/Dockerfile .
FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/egress-proxy ./cmd/egress-proxy

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/egress-proxy /egress-proxy
USER nonroot:nonroot
EXPOSE 3128
ENTRYPOINT ["/egress-proxy"]
