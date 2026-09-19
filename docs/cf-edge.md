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
| 证书登记 + `liyou/ops.admin` 绑定 | portal 证书库（服务器 SQLite `portal.db`，见 docs/database.md；JSON 旧档已迁移为 `.migrated`） | ✅ |
| Transform 透传规则 `ops-cert-passthrough` | API（phase `http_request_late_transform`，action `rewrite`，headers 为**对象**） | ✅ |
| Origin Pulls + nginx `ssl_verify_client on` | zone 全局 + `ORIGIN_PULL_VERIFY=on` | ✅ |

注意：CF 透传的 serial 是**大写** hex，建证 API 返回小写；后端用 `EqualFold` 比较。
无证请求时透传头为 `false`/空，源站查表不命中即 403。

| WAF 自定义规则 `ops-mtls-required` | API（phase `http_request_firewall_custom`，action `block`） | ✅ |

注意：CF 透传的 serial 是**大写** hex，建证 API 返回小写；后端用 `EqualFold` 比较。
无证请求时透传头为 `false`/空，源站查表不命中即 403。

## 边缘 WAF 拦截（已上线，实测）

规则 `ops-mtls-required`（动作 Block），无证/吊销证/非本账户 CA 证书在 CF 边缘
直接拦截（实测返回 CF "Attention Required" 拦截页，不再到达源站）。
上线时的规则文本（历史记录，注意彼时豁免了机器树路径）：

```text
(http.host in {"ops.yinghuoren.com"} and
  not starts_with(http.request.uri.path, "/api/agent/") and (
  not cf.tls_client_auth.cert_verified
  or cf.tls_client_auth.cert_revoked
  or cf.tls_client_auth.cert_issuer_ski ne "F275E69E4459913F80F4D9A36BA5AE9129C721C6"))
```

**2026-09-14 收紧**：`pki/cf_edge_setup.py` 的表达式本就无路径豁免（与
`docs/portal-agent-protocol.md`「不留免证路径」一致），重跑脚本重建规则后，
ops 域名全部路径（含 `/api/portal-agent/*`）在边缘强制 mTLS。机器请求
本就持机器证书过边缘，收紧只影响无证探测，不影响任何合法客户端。

两个实测踩坑：
- SKI 条件必须用 `ne` 放在 or 链里——无证请求的 issuer_ski 是空串，
  写成 `and ... eq "SKI"` 会恒假导致无证请求不匹配、规则失效。
- SKI 用**大写**（CF 官方示例格式），小写会导致有效证书被误拦。
- 机器（agent）同样持机器证书（portal「机器证书包」签发，kind=portal-agent），
  边缘对全部路径强制 mTLS，无免证路径；portal-agent 请求在 origin 侧再验
  「证书在表 + token 同 portal_agent_id」双因子。
- 发卡方 SKI 取自证书 Authority Key Identifier（非 CF API 返回的叶子 `ski` 字段）。

## 日常运维

- **token 白名单**：`cf_account_token` 已设 IP 白名单，仅可在服务器上调用 CF API；
  规则变更/重建须 SSH 到服务器执行（token 经 stdin 传入，不落盘）：
  `ssh workwork-server "cd ~/ops-portal && CF_RULES_TOKEN=\$(cat) python3 pki/cf_edge_setup.py" < ~/cf_token_and_cert/cf_account_token`
- 新设备：openssl 生成 CSR → API 签发（或面板 Add Certificate）→ portal 证书页登记 → 装证 → p12 按规范归档 `~/cf_token_and_cert/ops.yinghuoren.com/`（命名：用途_持有人-设备）。
- 吊销：CF 面板 Revoke（或 API 删除）+ portal 证书页置 revoked + 归档目录移除/标记对应 p12。
- 轮换记录：初代证书 `cert-mbpro-01`（serial `26B7…`）因私钥失效已双端吊销，
  由 `cert-mbpro-02`（serial `197FE164…`）接替，users.json 绑定随之换绑。
