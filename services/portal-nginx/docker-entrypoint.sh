#!/bin/bash
# portal-nginx 启动入口：渲染 nginx.conf.tmpl 并拉取 CF IP 段
# ORIGIN_PULL_VERIFY=on 时强制校验 Cloudflare 源站拉取证书；
# 本地 dev 自签联调用 off（生产必须 on）。
set -euo pipefail

OPS_DOMAIN="${OPS_DOMAIN:-ops.yinghuoren.com}"
ORIGIN_PULL_VERIFY="${ORIGIN_PULL_VERIFY:-off}"
CERT_VERIFY_HEADER="${CERT_VERIFY_HEADER:-X-Cert-Verify}"
CERT_SERIAL_HEADER="${CERT_SERIAL_HEADER:-X-Cert-Serial}"
CERT_SUBJECT_HEADER="${CERT_SUBJECT_HEADER:-X-Cert-Subject}"
CERT_FINGERPRINT_HEADER="${CERT_FINGERPRINT_HEADER:-X-Cert-Fingerprint}"

# 透传头字段名只允许 token 字符，防止注入 nginx 配置
for v in CERT_VERIFY_HEADER CERT_SERIAL_HEADER CERT_SUBJECT_HEADER CERT_FINGERPRINT_HEADER; do
  if ! [[ "${!v}" =~ ^[A-Za-z0-9-]+$ ]]; then
    echo "invalid header name in $v: ${!v}" >&2
    exit 1
  fi
done

if [ "$ORIGIN_PULL_VERIFY" != "on" ] && [ "$ORIGIN_PULL_VERIFY" != "off" ]; then
  echo "ORIGIN_PULL_VERIFY must be on|off, got: $ORIGIN_PULL_VERIFY" >&2
  exit 1
fi

if [ ! -f /etc/nginx/certs/server_cert.pem ] || [ ! -f /etc/nginx/certs/server_cert.key ]; then
  echo "missing /etc/nginx/certs/server_cert.pem(.key); run pki/dev-selfsign.sh first" >&2
  exit 1
fi

# header 名转 nginx $http_* 变量名：小写 + - 转 _
to_var() {
  printf 'http_%s' "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr '-' '_')"
}

rendered="$(cat /etc/nginx/templates/nginx.conf.tmpl)"
rendered="${rendered//__OPS_DOMAIN__/$OPS_DOMAIN}"
rendered="${rendered//__ORIGIN_PULL_VERIFY__/$ORIGIN_PULL_VERIFY}"
rendered="${rendered//__CERT_VERIFY_HEADER__/$CERT_VERIFY_HEADER}"
rendered="${rendered//__CERT_VERIFY_VAR__/$(to_var "$CERT_VERIFY_HEADER")}"
rendered="${rendered//__CERT_SERIAL_HEADER__/$CERT_SERIAL_HEADER}"
rendered="${rendered//__CERT_SERIAL_VAR__/$(to_var "$CERT_SERIAL_HEADER")}"
rendered="${rendered//__CERT_SUBJECT_HEADER__/$CERT_SUBJECT_HEADER}"
rendered="${rendered//__CERT_SUBJECT_VAR__/$(to_var "$CERT_SUBJECT_HEADER")}"
rendered="${rendered//__CERT_FINGERPRINT_HEADER__/$CERT_FINGERPRINT_HEADER}"
rendered="${rendered//__CERT_FINGERPRINT_VAR__/$(to_var "$CERT_FINGERPRINT_HEADER")}"
printf '%s' "$rendered" > /etc/nginx/conf.d/default.conf

# 拉取 CF IP 段（失败则脚本内写内置 fallback，保证 real_ip 可用）
if ! /usr/local/bin/fetch-cloudflare-ips.sh; then
  echo "warning: CF IP fetch failed, check /etc/nginx/cloudflare-ips.conf" >&2
fi

nginx -t
exec "$@"
