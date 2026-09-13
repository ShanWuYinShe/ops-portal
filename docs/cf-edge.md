# Cloudflare 边缘配置（ops.yinghuoren.com）— 实际落地记录

> 2026-09-14 全部落地并实测通过。多数步骤已 API 自动化（`pki/cf_edge_setup.py`）。

## 认证链（实测验证）

```
浏览器（装设备证书）
  → CF 边缘：验证证书（托管 CA，主机关联已启用）
  → Transform 规则逐请求注入 X-Cert-Verify/Serial/Subject/Fingerprint
  → 源站 nginx（Origin Pulls，直连 400 拒）原样透传
  → portal 后端：serial/指纹查本地证书表（active）→ 200 / 不命中 403
```

实测：有证 whoami 200（liyou/ops.admin）；无证 403；直连源站 IP 400。

## 已落地的项

| 项 | 方式 | 状态 |
|---|---|---|
| DNS AAAA → `::30` 橙云 | API | ✅ |
| mTLS 主机关联（托管 CA） | `PUT .../certificate_authorities/hostname_associations` | ✅ |
| 设备证书 `liyou-mbpro`（serial `197FE164…`，至 2028-12-16） | API 签发（CSR 本地生成），p12 归档 `~/cf_token_and_cert/ops.yinghuoren.com/` | ✅ |
| 证书登记 + `liyou/ops.admin` 绑定 | portal 证书库（服务器 `.workwork/data/certs.json`、`users.json`） | ✅ |
| Transform 透传规则 `ops-cert-passthrough` | API（phase `http_request_late_transform`，action `rewrite`，headers 为**对象**） | ✅ |
| Origin Pulls + nginx `ssl_verify_client on` | zone 全局 + `ORIGIN_PULL_VERIFY=on` | ✅ |

注意：CF 透传的 serial 是**大写** hex，建证 API 返回小写；后端用 `EqualFold` 比较。
无证请求时透传头为 `false`/空，源站查表不命中即 403。

## 可选加固（token 无权限，面板手工可加）

WAF 自定义规则（边缘直接拦无证/吊销证，省源站开销）：

```text
(http.host eq "ops.yinghuoren.com" and
 (not cf.tls_client_auth.cert_verified or cf.tls_client_auth.cert_revoked) and
 cf.tls_client_auth.cert_issuer_ski eq "f275e69e4459913f80f4d9a36ba5ae9129c721c6")
```

动作 Block。发卡方 SKI 取自证书 Authority Key Identifier（非 CF API 的叶子 `ski` 字段）。
不加也安全：源站证书表已拒无证/未知/吊销证。

## 日常运维

- 新设备：openssl 生成 CSR → API 签发（或面板 Add Certificate）→ portal 证书页登记 → 装证 → p12 按规范归档 `~/cf_token_and_cert/ops.yinghuoren.com/`（命名：用途_持有人-设备）。
- 吊销：CF 面板 Revoke（或 API 删除）+ portal 证书页置 revoked + 归档目录移除/标记对应 p12。
- 轮换记录：初代证书 `cert-mbpro-01`（serial `26B7…`）因私钥失效已双端吊销，
  由 `cert-mbpro-02`（serial `197FE164…`）接替，users.json 绑定随之换绑。
- 规则变更/重建：`CF_RULES_TOKEN=$(cat ~/cf_token_and_cert/cf_account_token) python3 pki/cf_edge_setup.py`（幂等）。
