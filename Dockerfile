# syntax=docker/dockerfile:1

# ---------- 前端构建 ----------
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---------- 后端构建 (内嵌前端产物) ----------
FROM golang:1.27-alpine AS build
ARG VERSION=0.1.0
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf internal/web/build && mkdir -p internal/web/build
COPY --from=web /src/web/build/. ./internal/web/build/
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/yeqingky/Picsky/internal/config.Version=${VERSION}" \
    -o /out/picsky ./cmd/picsky

# ---------- 运行时 ----------
FROM alpine:3.22
LABEL org.opencontainers.image.title="Picsky" \
      org.opencontainers.image.description="图片上传中间层与管理系统" \
      org.opencontainers.image.source="https://github.com/yeqingky/Picsky"

RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -g 1000 picsky \
    && adduser -D -u 1000 -G picsky picsky \
    && mkdir -p /data/tmp /app \
    && chown -R picsky:picsky /data /app

COPY --from=build /out/picsky /usr/local/bin/picsky

USER picsky
WORKDIR /app
VOLUME ["/data"]
EXPOSE 5214

ENV PICSKY_LISTEN=0.0.0.0:5214 \
    PICSKY_DB_DRIVER=sqlite \
    PICSKY_DB_DSN=/data/picsky.db \
    PICSKY_TEMP_DIR=/data/tmp \
    PICSKY_API_PREFIX=/api/v1 \
    PICSKY_LSKY_COMPAT_ENABLED=true \
    PICSKY_LSKY_COMPAT_PREFIX=/lsky \
    PICSKY_UPLOAD_MEMORY_LIMIT=8388608 \
    PICSKY_LOG_LEVEL=info

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:5214/api/v1/health || exit 1

ENTRYPOINT ["/usr/local/bin/picsky"]
