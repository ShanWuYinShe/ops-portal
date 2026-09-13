ARG NGINX_VERSION=1.30.4

FROM nginx:${NGINX_VERSION}-alpine

RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories \
    && apk add --no-cache curl bash

# Cloudflare Origin Pull 证书：构建时从 CF 官方拉取并校验 PEM 结构，
# 与 workwork 的 services/nginx/Dockerfile 做法一致。
RUN curl -fsSL -o /etc/nginx/cloudflare-origin-pull.pem \
        https://developers.cloudflare.com/ssl/static/authenticated_origin_pull_ca.pem \
    && grep -q -- "-----BEGIN CERTIFICATE-----" /etc/nginx/cloudflare-origin-pull.pem \
    && grep -q -- "-----END CERTIFICATE-----" /etc/nginx/cloudflare-origin-pull.pem

COPY nginx.conf.tmpl /etc/nginx/templates/nginx.conf.tmpl
COPY fetch-cloudflare-ips.sh /usr/local/bin/fetch-cloudflare-ips.sh
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /usr/local/bin/fetch-cloudflare-ips.sh /docker-entrypoint.sh

ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["nginx", "-g", "daemon off;"]
