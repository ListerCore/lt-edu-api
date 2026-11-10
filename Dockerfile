# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/main ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.24 AS runtime

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app

ENV TZ=America/Lima

WORKDIR /app

COPY --from=builder /out/main ./main
COPY --from=builder /out/migrate ./migrate
COPY entrypoint.sh ./entrypoint.sh

RUN chmod +x ./main ./migrate ./entrypoint.sh && \
    chown -R app:app /app

USER app
# HTTP API and gRPC (APP__HOST__PORT, APP__GRPC__PORT); both internal only
EXPOSE 8080 9090

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q --spider "http://127.0.0.1:${APP__HOST__PORT:-8080}/api/v1/health" || exit 1

# Pending migrations first, then the server (see entrypoint.sh)
ENTRYPOINT ["./entrypoint.sh"]
