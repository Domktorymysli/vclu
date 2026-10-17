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

RUN apk add --no-cache ca-certificates tzdata su-exec \
 && addgroup -g 1000 vclu \
 && adduser -D -H -u 1000 -G vclu -s /sbin/nologin vclu \
 && mkdir -p /data /opt/vclu/data \
 && chown vclu:vclu /data /opt/vclu/data

# Install directory
WORKDIR /opt/vclu

# Binary
COPY --from=builder /vclu ./vclu
# Port 69/udp (TFTP) is below 1024: the binary may bind it without root
RUN apk add --no-cache --virtual .setcap libcap-utils \
 && setcap cap_net_bind_service=+ep /opt/vclu/vclu \
 && apk del .setcap

# Web assets (templates, static, locales)
COPY web/ ./web/

# Lua runtime (core objects, bootstrap, integrations)
COPY pkg/lua/runtime/ ./runtime/

# AI docs (MCP read_file)
COPY docs/ai/ ./docs/ai/
COPY --chmod=755 deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# Data volume — persists config, Lua scripts, backups
VOLUME /data

# Web UI
EXPOSE 8080
# UDP command server
EXPOSE 1234/udp
# TFTP server
EXPOSE 69/udp

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:$(cat /tmp/vclu-web-port 2>/dev/null || echo 8080)/api/health" || exit 1

# Starts as root only to fix /data and GPIO, then runs vCLU as vclu (uid 1000)
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["--auto", "--data-dir", "/data", "--web-port", "8080"]
