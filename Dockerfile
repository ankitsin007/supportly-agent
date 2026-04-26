# syntax=docker/dockerfile:1.7
#
# Multi-stage build for ghcr.io/ankitsin007/supportly-agent.
#
# Stage 1 (builder): compile a static binary from current source.
# Stage 2 (runtime): distroless static. Final image ~25 MB; no shell, no
#   package manager, nothing CVE-scannable but Go itself.
#
# The release workflow uses `docker buildx build` with this Dockerfile
# to produce multi-arch (linux/amd64 + linux/arm64) images and pushes
# them to ghcr.io. The Go cross-compile happens inside the builder
# stage thanks to BuildKit's TARGETOS/TARGETARCH automatic args.

FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# BuildKit injects TARGETOS + TARGETARCH for the requested platform when
# `docker buildx build --platform linux/amd64,linux/arm64 ...` is used.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o /out/supportly-agent ./cmd/supportly-agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/supportly-agent /usr/local/bin/supportly-agent

# Container metadata (consumed by ghcr.io UI, vulnerability scanners, etc.).
LABEL org.opencontainers.image.source="https://github.com/ankitsin007/supportly-agent"
LABEL org.opencontainers.image.description="Drop-in error capture agent for Supportly. No SDK required."
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.vendor="Supportly"

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/supportly-agent"]
