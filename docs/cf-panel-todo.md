# CF 面板手工配置（token 无 rulesets 权限，必须人工点）

> doing：以下两件做完，ops.yinghuoren.com 即完整 mTLS。
> token（cf_account_token）已验证：DNS / 证书签发吊销 / 旧 firewall 可调；
> `rulesets` 系（WAF 自定义规则 / Transform Rules）报 10000 认证错，只能面板手工建。

## 件一：WAF 自定义规则（无有效证书即 Block）

位置：Cloudflare 面板 → yinghuoren.com → Security → WAF → Custom rules → Create rule。

- Rule name：`ops-mtls-required`
- Field 表达式（Expression Builder 切到 Edit expression 粘贴）：

```text
(http.host eq "ops.yinghuoren.com" and (not cf.tls_client_auth.cert_verified or cf.tls_client_auth.cert_revoked))
```

- Action：Block。
- 建议再加发卡方 pin（防任意 CA 有效证书混入），追加 and 条件：

```text
cf.tls_client_auth.cert_issuer_ski eq "f275e69e4459913f80f4d9a36ba5ae9129c721c6"
```

（发卡方 SKI，取自证书 Authority Key Identifier 去冒号小写；
注意不是 CF API 返回的叶子 `ski` 字段。）

验证：无证访问 `https://ops.yinghuoren.com/api/portal/agents` 应变 CF 默认 Block
页（边缘拦，连 401 JSON 都看不到）；有证访问恢复 401（源站门禁）→ 配完件二变 200。

## 件二：Transform Rules（证书信息透传给源站）

位置：Rules → Transform Rules → Modify Request Header → Create rule。

- Rule name：`ops-cert-passthrough`
- When：`http.host eq "ops.yinghuoren.com"`。
- Then Set static/dynamic headers（Dynamic 值，字段名以面板实际变量名为准，
  来自官方 client-certificate-variables 文档）：

| Header | Dynamic 值 |
|---|---|
| `X-Cert-Verify` | `cf.tls_client_auth.cert_verified`（若面板无布尔转字符串，改用 `cf.tls_client_auth.cert_presented` 或固定 `"SUCCESS"` 占位并备注） |
| `X-Cert-Serial` | `cf.tls_client_auth.cert_serial` |
| `X-Cert-Subject` | `cf.tls_client_auth.cert_subject_dn` |
| `X-Cert-Fingerprint` | `cf.tls_client_auth.cert_fingerprint_sha256` |

- 注意：源站 nginx 原样透传，后端只认受信代理（portal-nginx）的头；
  客户端自带同名头不可信（真实性来自 Origin Pulls）。

验证：有证访问 `https://ops.yinghuoren.com/api/portal/whoami` 应 200，
返回 `username: liyou / role: ops.admin / serial: 26b759...`。

## 已用 API 落地的（无需手工）

- DNS：`ops.yinghuoren.com AAAA → 2605:a141:2322:2623:1::30（橙云）`。
- mTLS 主机关联：`ops.yinghuoren.com` 已关联托管 CA。
- 首张设备证书：`liyou-mbpro`（CF id `044db110…`，serial `26b759…`，
  指纹 `7f0258…`，有效期至 2028-12-16），p12 在本机钥匙串（密码另记），
  已登记进 portal 证书库并绑定 `liyou/ops.admin`。
- 源站：Origin Pulls on + nginx `ssl_verify_client on`；直连源站 IP 400 拒绝已验证。
