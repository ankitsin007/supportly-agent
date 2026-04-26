# syntax=docker/dockerfile:1.7
#
# Multi-stage build for ghcr.io/ankitsin007/supportly-agent.
#
# Stage 1 (builder): compile a static binary from current source. Used by
# `docker build` invocations outside of GoReleaser (e.g., local testing).
# In production, GoReleaser pre-builds binaries and the release workflow's
# `docker buildx` consumes them via build args — see .goreleaser.yml.
#
# Stage 2 (runtime): distroless static. ~25 MB total image, no shell, no
# package manager, nothing to CVE-scan but Go itself.
#
# To build locally:
#   docker build -t supportly-agent:dev .
# To use a pre-built binary (release path):
#   docker build --build-arg PREBUILT=./bin/supportly-agent-linux-amd64 -t supportly-agent .

ARG PREBUILT=""

FROM golang:1.22-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o /out/supportly-agent ./cmd/supportly-agent

# Tiny copy stage that selects either the pre-built binary (release path)
# or the freshly-compiled one (local-dev path). Keeps the runtime stage
# uncluttered.
FROM alpine:3.20 AS chooser
ARG PREBUILT
COPY --from=builder /out/supportly-agent /tmp/built
COPY ${PREBUILT:-/tmp/built} /tmp/agent
RUN chmod +x /tmp/agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=chooser /tmp/agent /usr/local/bin/supportly-agent

# Container metadata (consumed by ghcr.io UI, vulnerability scanners, etc.).
LABEL org.opencontainers.image.source="https://github.com/ankitsin007/supportly-agent"
LABEL org.opencontainers.image.description="Drop-in error capture agent for Supportly. No SDK required."
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.vendor="Supportly"

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/supportly-agent"]
