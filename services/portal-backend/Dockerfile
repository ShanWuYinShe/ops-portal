ARG GO_VERSION=1.26.10
ARG BUN_VERSION=1.3.14
ARG ALPINE_VERSION=3.24.1

FROM oven/bun:${BUN_VERSION}-alpine AS ui-builder

# 前端版本标识（git SHA），由 compose 从部署环境注入；显示在侧栏底部
ARG UI_VERSION=dev
ENV UI_VERSION=${UI_VERSION}

WORKDIR /ui
COPY ui/package.json ui/bun.lock ./
RUN bun install --frozen-lockfile
COPY ui/ ./
RUN bun run build

FROM golang:${GO_VERSION}-alpine AS go-builder

RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache git

WORKDIR /build

ARG GOPROXY
ENV GOPROXY=${GOPROXY:-https://goproxy.cn,direct}

COPY services/portal-backend/go.mod services/portal-backend/go.sum ./
RUN go mod download

COPY services/portal-backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o portal-backend .

FROM alpine:${ALPINE_VERSION}

ARG RUN_UID=1001
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates wget \
    && adduser -D -u ${RUN_UID} appuser \
    && mkdir -p /app/data /app/ui \
    && chown -R appuser:appuser /app

COPY --from=go-builder /build/portal-backend /app/portal-backend
COPY --from=ui-builder /ui/build /app/ui

WORKDIR /app
USER appuser
ENTRYPOINT ["/app/portal-backend"]
