# Cloudflare 边缘配置清单（ops.yinghuoren.com）

> 人工在 CF 面板执行一次。字段名以面板实际文案对齐，规则生效后回填"已验证"。

## 1. DNS

- `ops.yinghuoren.com` → 源站，**橙云（已代理）**。
- SSL/TLS 加密模式：Full (strict)。

## 2. 客户端证书（SSL/TLS → Client Certificates）

- 用 Cloudflare 托管 CA 签发设备证书（免费版只能走这条；BYOCA 需 Enterprise）。
- 每台设备一张：CN/备注写 `持有人-设备名`（如 `liyou-mbpro`）。
- 记录每张证的序列号 / 指纹 / 有效期，供源站查表。

## 3. WAF 自定义规则（Security → WAF → Custom rules）

规则：主机 `ops.yinghuoren.com` 且无有效证书即 Block。

表达式（字段名来自官方文档）：

```text
(http.host eq "ops.yinghuoren.com" and
 (not cf.tls_client_auth.cert_verified or cf.tls_client_auth.cert_revoked))
```

动作：Block。建议再叠加发卡方 SKI pin：

```text
cf.tls_client_auth.cert_issuer_ski eq "<托管CA的SKI>"
```

SKI 取法（官方）：`openssl x509 -noout -ext authorityKeyIdentifier -in mtls.crt | tail -n1 | tr -d ': '`。

## 4. 证书透传（Transform Rules → Request Header Modification）

边缘向源站注入（字段名以面板实际为准）：

| 请求头 | 值来源 |
|---|---|
| `X-Cert-Verify` | 证书验证结果（verified/ revoked 标记） |
| `X-Cert-Serial` | `cf.tls_client_auth.cert_serial` |
| `X-Cert-Subject` | `cf.tls_client_auth.cert_subject_dn` |
| `X-Cert-Fingerprint` | `cf.tls_client_auth.cert_fingerprint_sha256` |

源站 nginx 原样透传，portal-backend 按受信代理 + 本地有效证书表二次确认。
注意：客户端自带的同名头不可信——真实性来自 Origin Pulls（只有 CF 能连源站）。

## 5. 源站保护（SSL/TLS → Origin Server）

- Authenticated Origin Pulls：Per-hostname 给 `ops.yinghuoren.com` 开 on。
- nginx 侧 `ssl_verify_client on` 已在模板里（`ORIGIN_PULL_VERIFY=on`）。

## 6. 验证步骤

1. 无证访问 `https://ops.yinghuoren.com/` → 边缘 403（连源站日志都没有）。
2. 有证访问 → 200，且源站日志/ whoami 能看到 subject/serial。
3. 面板吊销一张证 → 该设备立刻 403，其他设备正常。
