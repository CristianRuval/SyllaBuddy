# syntax=docker/dockerfile:1

# --- Stage 1: Build static Go monolithic binary ---
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache Go module downloads separately from source changes
COPY go.mod go.sum* ./
RUN go mod download

# Copy Go source, internal packages, and embedded web assets
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /syllabuddy ./cmd/syllabuddy

# --- Stage 2: Minimal runtime for GCP e2-micro ---
FROM alpine:3.21

RUN apk add --no-cache \
        poppler-utils \
        ca-certificates \
        tzdata \
    && addgroup -S -g 10001 appgroup \
    && adduser -S -D -H -u 10001 -G appgroup appuser \
    && mkdir -p /app/uploads /app/data \
    && chown -R appuser:appgroup /app

WORKDIR /app

COPY --from=builder --chown=appuser:appgroup /syllabuddy /app/syllabuddy

ENV PORT=8080 \
    DB_PATH=/app/data/syllabuddy.db \
    UPLOAD_DIR=/app/uploads

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-8080}/healthz" || exit 1

USER appuser

ENTRYPOINT ["/app/syllabuddy"]
