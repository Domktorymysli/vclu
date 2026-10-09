# ── Build stage ──────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags="-w -s -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /vclu \
    ./cmd/vclu

# ── Runtime stage ────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

# Install directory
WORKDIR /opt/vclu

# Binary
COPY --from=builder /vclu ./vclu

# Web assets (templates, static, locales)
COPY web/ ./web/

# Lua runtime (core objects, bootstrap, integrations)
COPY pkg/lua/runtime/ ./runtime/

# AI docs (MCP read_file)
COPY docs/ai/ ./docs/ai/

# Data volume — persists config, Lua scripts, backups
VOLUME /data

# Web UI
EXPOSE 8080
# UDP command server
EXPOSE 1234/udp
# TFTP server
EXPOSE 69/udp

ENTRYPOINT ["/opt/vclu/vclu"]
CMD ["--auto", "--data-dir", "/data", "--web-port", "8080"]
