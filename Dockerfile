# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Cache go mod download as a separate layer — only re-runs when go.mod changes
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build gateway binary
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /out/rah-gateway ./cmd/rah-gateway/

# ── Stage 2: Gateway runtime ───────────────────────────────────────────────────
FROM --platform=$TARGETPLATFORM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder /usr/share/zoneinfo              /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/rah-gateway                 /usr/local/bin/rah-gateway

WORKDIR /app
EXPOSE 8080 8081
USER nonroot:nonroot

HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/rah-gateway", "-health", "-mport", "8081"]

ENTRYPOINT ["/usr/local/bin/rah-gateway"]
CMD ["-port", "8080", "-mport", "8081", "-config", "/app/gateway.yaml"]
