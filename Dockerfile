# ── Stage 1: Build ────────────────────────────────────────────────────────────
# Use the official Go image matching go.mod version.
# BUILDPLATFORM = the machine running Docker (usually amd64).
# TARGETOS/TARGETARCH = the final image target (set by --platform flag).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

# Install git (needed by go modules that import via VCS)
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Copy dependency manifests first — Docker layer-caches this until go.mod changes.
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Cross-compile for the requested target platform.
# CGO_ENABLED=0 produces a fully static binary — no glibc dependency.
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
      -ldflags="-s -w" \
      -trimpath \
      -o /out/rah-gateway \
      ./cmd/rah-gateway/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
      -ldflags="-s -w" \
      -trimpath \
      -o /out/rah-studio \
      ./cmd/rah-studio/

# ── Stage 2: Minimal runtime image ────────────────────────────────────────────
# distroless/static-debian12 has no shell, no package manager — smallest attack surface.
# Use :nonroot variant to run as uid 65532 automatically.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# Copy timezone data and CA certs from builder (needed for HTTPS + time zones)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy binaries
COPY --from=builder /out/rah-gateway  /usr/local/bin/rah-gateway
COPY --from=builder /out/rah-studio   /usr/local/bin/rah-studio

# Default config location (mount your own gateway.yaml here)
WORKDIR /app

# Gateway port  = 8080
# Management port = 8081
EXPOSE 8080 8081

# Run as non-root (uid 65532 from distroless:nonroot)
USER nonroot:nonroot

# Health check — hits the management plane which starts immediately
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/rah-gateway", "-health"]

ENTRYPOINT ["/usr/local/bin/rah-gateway"]
CMD ["-port", "8080", "-mport", "8081", "-config", "/app/gateway.yaml"]
