# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/main ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrations

FROM alpine:3.24 AS runtime

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app

COPY --from=builder /out/main ./main
COPY --from=builder /out/migrate ./migrate
COPY migrations/ ./migrations/
COPY entrypoint.sh ./entrypoint.sh

RUN chmod +x ./entrypoint.sh ./main ./migrate && \
    chown -R app:app /app

USER app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q --spider "http://127.0.0.1:${APP_PORT:-8080}/api/v1/health" || exit 1

ENTRYPOINT ["./entrypoint.sh"]
