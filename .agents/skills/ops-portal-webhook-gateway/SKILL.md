---
name: ops-portal-webhook-gateway
description: ops-portal webhook 网关的定位、门禁、消费方接入与机器证书签发实操。触发：接入新的 webhook 消费方、修改 /api/hooks 接收端点、签发 kind=service 证书、配置告警 ALERT_WEBHOOK、排查 webhook 投递状态。
---

# ops-portal webhook 网关

## 定位与门禁

- 消费方**仅限本 portal 管理的自建站点**，全部可持登记证书；不存在无法持证的第三方。
- `POST /api/hooks/{slug}` 与其他业务路径一致套 `requireCert`（commit 6632bde），边缘对全部路径强制 mTLS（cf-edge.md），**无免证路径**。
- 反模式：按「公网第三方回调」做成免证 + HMAC。真实消费方全部自建，门禁统一为 `requireCert`；HMAC secret 仅保留为可选第二层。
- 端点未配 secret 时要求请求来自受信代理（经 nginx 即满足）；`reject_reason` 为 `no enabled source` 且 `status=delivered` = 只收不转发的正常态（无转发规则），需要转发到飞书/钉钉时在管理台给端点加 source 规则。

## 已接入消费方

| 站点 | 场景 | 状态 |
|---|---|---|
| hotspot | 巡检/备份告警（crontab `ALERT_WEBHOOK` 前缀） | 已接入，实测 202 |
| workwork | `backup.sh` / `deploy-changed.sh` 失败通知 + admin Go 事件 | 已合入并部署（`deploy-changed.sh` 复跑模式重建 admin，v1.1.0-67-g59601ab 指纹核对通过）；`backup.sh` 真实 failure 事件实测 delivered |

- 端点：slug=`alerts`（id wh-ep-7b84acb8）；脚本侧证书默认 `~/.webhook-caller/{cert,key}.pem`，可用 `ALERT_MTLS_CERT/KEY` 覆盖；证书文件缺失时回退无证（兼容钉钉/飞书直推）。
- hotspot 的 `scripts/deploy-production.sh` 里 `ALERT_WEBHOOK` 在**部署机（Mac）**环境取值，服务器 crontab 管不到。

### backup.sh 退出码语义（rc=1 按警告处理，workwork 9bb7f1a）

`.wework` 运行时目录归档期间被写入时，GNU tar 以 rc=1（file changed）退出，EXIT trap 会把每次例行备份误报成 `backup.failure`。实现按 rc 分流：rc=1（按读取时点快照，归档完整可用）降级为警告并继续，rc>=2 才算失败进入 missing。验证方式：桩 tar 覆盖 rc=1/rc=2 双分支；生产环境备份 RC=0、恢复验证通过、投递记录零新增。

**注意**：给「退出码即告警」的脚本接 webhook 前，先区分工具的「警告级」退出码。

## kind=service 机器证书签发实操

1. 服务器上生成密钥与 CSR：`openssl req -new -newkey rsa:2048 -nodes -keyout
   ~/.webhook-caller/key.pem -out /tmp/x.csr -subj /CN=ops-alerts`（0600 密钥）。
2. CF API 签发（凭据在服务器 `~/ops-portal/.env` 的 `CF_API_TOKEN`/`CF_ZONE_ID`，
   token 有 IP 白名单仅服务器可用；与 `agentcerts.go` 的 `cfIssueClientCert` 同款）：
   `POST https://api.cloudflare.com/client/v4/zones/{zone}/client_certificates`，
   body `{"validity_days":825,"csr":"..."}`。
   **坑（400 code 1462 "Failed to parse input CSR"）**：必须像 Go 版一样
   把 PEM 真实换行替换为字面反斜杠+n 后**手工拼 JSON 字符串**；若用 `json.dumps`，
   已替换的 `\n` 会被再转义一层导致 CF 解析 CSR 失败。
3. portal 登记（路由为 `requireRole("ops.admin")`，持该角色证书如 `~/.backup-caller` 调用）：
   `POST /api/portal/certs/register`，body `{label,kind:"service",serial(大写),
   fingerprint(小写),cf_id,common_name,status:"active",expires_at,note}`。
4. 证书产物落 `~/.webhook-caller/cert.pem`（0600）。

## 验证命令（服务器上）

```sh
# 持证推送（期望 202）
curl -s --cert ~/.webhook-caller/cert.pem --key ~/.webhook-caller/key.pem \
  -H 'Content-Type: application/json' \
  -d '{"text":"verify"}' https://ops.yinghuoren.com/api/hooks/alerts
# 无证推送（期望边缘 403）
curl -s -o /dev/null -w '%{http_code}\n' -d '{}' https://ops.yinghuoren.com/api/hooks/alerts
# 投递记录（admin 证书）
curl -s --cert ~/.backup-caller/cert.pem --key ~/.backup-caller/key.pem \
  'https://ops.yinghuoren.com/api/portal/webhooks/deliveries?limit=2'
```

crontab 点亮方式：在命令前加 `ALERT_WEBHOOK="https://ops.yinghuoren.com/api/hooks/alerts" ` 前缀（已备份 `~/crontab.bak.webhook`）。

## 门户自身告警的出站渠道

- 与上面的「入站接收」是两条链路：门户**自己**产生的告警（agent 掉线/恢复、拨测不可达/恢复/降级）走 `ALERT_WEBHOOK_URL`（可选 `ALERT_WEBHOOK_SECRET` 做 HMAC-SHA256 → 头 `X-Webhook-Signature: sha256=<hex>`），payload 为 `{event,subject,detail,text,time}`；不经 `webhook_deliveries`，故没有重试/重放，重复抑制靠 `ALERT_COOLDOWN`（默认 30m）。实现见 `services/portal-backend/alertwebhook.go`，文档见 `docs/portal-agent-protocol.md`「心跳超时告警」。
- 目标过 SSRF 校验：私网默认拒绝，纯内网联调需 `OPS_PROBE_ALLOW_PRIVATE=1`（与拨测/webhook 转发共用同一逃生阀）。
- 验证方式（容器内联调）：登记 `kind=portal-agent` 证书 + 签发 agent token → 心跳数轮（覆盖 watchdog 基线轮）→ 停发 → 10s 后（`AGENT_OFFLINE_AFTER=10s`）watchdog 写 `alert.agent_offline`，sink 收到 `POST /hook`，头 `X-Webhook-Event: agent_offline` + 签名，body 四字段齐备；签名以 `HMAC-SHA256("verify-secret", body)` 独立复算一致。单测见 `services/portal-backend/alertwebhook_test.go`。
- **坑**：dev 免证容器里 `TRUSTED_PROXY_CIDRS=0.0.0.0/0` 会把 agent 请求判成「受信代理但无证书」→ `agent certificate mismatch`，心跳被拒、watchdog 永远看不到 agent（表现为「告警不触发」）。本机复现要么不放宽受信网段，要么先登记 `kind=portal-agent` 证书再带 `X-Cert-Serial`/`X-Cert-Fingerprint` 心跳。
