# syntax=docker/dockerfile:1.7

# Stage 1: Build React frontend
# TODO: pin by digest -> node:22-alpine@sha256:<digest>
FROM node:22-alpine AS frontend
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
# TODO: pin by digest -> golang:1.23-alpine@sha256:<digest>
FROM golang:1.23-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# Copy only what the Go build needs; .dockerignore excludes web/node_modules,
# data/, .git, planning docs, etc.
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=frontend /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/cartledger ./cmd/server

# Stage 3: Minimal runtime
# TODO: pin by digest -> alpine:3.20@sha256:<digest>
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -g 10001 -S cartledger \
    && adduser -u 10001 -S -G cartledger -h /home/cartledger cartledger \
    && mkdir -p /data \
    && chown -R cartledger:cartledger /data

COPY --from=backend --chown=cartledger:cartledger /out/cartledger /usr/local/bin/cartledger

# Sensible defaults — deployer can override via env / compose.
ENV PORT=8079 \
    DATA_DIR=/data

USER 10001:10001

# Explicit mount point for persistent state (SQLite DB + receipt images)
VOLUME ["/data"]

EXPOSE 8079

# /livez is exposed by the Go server (added by B1). wget is present in alpine
# via the apk install above; distroless would require dropping this and
# relying on the compose-level healthcheck instead.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${PORT}/livez" || exit 1

ENTRYPOINT ["cartledger"]
