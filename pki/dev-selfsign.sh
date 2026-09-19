#!/bin/bash
# 本地 dev 自签证书：只用于 https://localhost:8443 联调，
# 生产走 CF 边缘证书 + 源站 Origin 证书，此脚本永不用于生产。
set -euo pipefail

OUT_DIR="${1:-./cert}"
mkdir -p "$OUT_DIR"
chmod 700 "$OUT_DIR"

openssl req -x509 -newkey rsa:2048 -sha256 -days 30 -nodes \
  -keyout "$OUT_DIR/server_cert.key" \
  -out "$OUT_DIR/server_cert.pem" \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

chmod 600 "$OUT_DIR/server_cert.key"
chmod 644 "$OUT_DIR/server_cert.pem"
echo "dev cert written to $OUT_DIR/server_cert.pem(.key), valid 30 days"
