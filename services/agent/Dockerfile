ARG GO_VERSION=1.26.5
ARG ALPINE_VERSION=3.24.1

FROM golang:${GO_VERSION}-alpine AS go-builder

RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache git

WORKDIR /build

ARG GOPROXY
ENV GOPROXY=${GOPROXY:-https://goproxy.cn,direct}

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ops-agent .

FROM alpine:${ALPINE_VERSION}

RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates git \
    && adduser -D -u 1001 appuser

COPY --from=go-builder /build/ops-agent /app/ops-agent

USER appuser
ENTRYPOINT ["/app/ops-agent"]
