# Webhook 网关

接收**本 portal 管理的自建站点**推送的运维事件（hotspot 巡检/备份告警、workwork
备份/部署通知等），可选签名校验 → 落库留痕 → 按规则转发到通知目标 → 失败重试 →
全量投递日志，管理台可查询与重放。调用方统一持登记证书过边缘 mTLS。

与 portal 其余部分**同进程、同 SQLite、同 compose**：并入现有
`services/portal-backend` 容器（不新增容器、不新增端口），管理接口沿用现有证书门禁
与 `requireRole` RBAC，出站转发复用 `ssrfguard.go` 的 SSRF 防线。

## 数据模型

新增 4 张表（建表在 `db.go` 的 schema 常量；时间一律
`time.Now().UTC().Format(time.RFC3339)`）：

| 表 | 说明 |
|---|---|
| `webhook_endpoints` | 接收端点：`slug` 全局唯一（即接收路径段） |
| `webhook_sources` | 转发规则/目标：一个端点可挂多条规则 |
| `webhook_deliveries` | 入站投递记录（原文全量落库，便于排障与重放） |
| `webhook_attempts` | 出站转发尝试（每次尝试一行，含请求/响应体） |

`webhook_endpoints`：`id`（`wh-ep-` + 8 位 hex）、`name`、`slug`（UNIQUE）、
`secret`、`enabled`、`note`、`created_at`、`created_by`。
更新（带 `id`）不改 `created_at` / `created_by`，保留首次落库值供审计。

`webhook_sources`：`id`（`wh-src-` + 8 位 hex）、`endpoint_id`、`name`、
`target_url`、`secret`（出站签名用）、`enabled`、`max_attempts`（默认 3）、
`timeout_ms`（默认 10000）、`created_at`。

`webhook_deliveries`：`id`（`wh-dlv-` + 16 位 hex）、`endpoint_id`、`slug`、
`received_at`、`remote_ip`、`method`、`content_type`、`headers`（JSON 文本）、
`body`（原文）、`body_bytes`、`sig_ok`、`status`、`reject_reason`、
`event_type`、`dedup_key`。

`webhook_attempts`：`id`（INTEGER 自增）、`delivery_id`、`source_id`、
`attempt_no`、`status`、`http_status`、`error`、`duration_ms`、
`request_body`、`response_body`、`at`。

### 投递状态（deliveries.status）

| 值 | 含义 |
|---|---|
| `pending` | 已落库待转发（转发 worker 的扫描目标） |
| `delivered` | 全部启用规则转发成功 |
| `failed` | 超过 `max_attempts` 仍未成功 |
| `rejected` | 入站即拒（签名缺失/不匹配等），不参与转发 |
| `dedup` | 保留值：**当前实现不会写入该状态**（见下） |

**这五个值是白名单**：`deliveries` 列表接口的 `status` 过滤参数不在白名单内直接
返回 400（不接受任意串）。

`dedup` 的实际语义：命中幂等（同 endpoint + `dedup_key` 已存在）时，接收端点**直接
返回 200 `{"dedup":true,"delivery_id":"<既有 id>"}` 且不新建任何记录**——既不复写原
记录的 status，也不插入新行。因此该值在列表里查不到行；它保留在白名单内，是给
「按状态过滤」的调用方做向前兼容（若将来改为落一条去重留痕行，过滤参数无需再改）。

### 索引

`webhook_endpoints(slug)` 建表已含 UNIQUE；另建
`webhook_deliveries(status)`、`webhook_deliveries(endpoint_id, received_at)`
（列表按端点+时间倒序分页）、`webhook_deliveries(dedup_key)`、
`webhook_attempts(delivery_id)`、`webhook_sources(endpoint_id)`。

### 级联与保留

- 删除端点（`endpoints/remove`）在事务内**级联删其 sources**；该端点的
  `deliveries` / `attempts` **保留**作历史排障记录。
- webhook 数据有**独立的保留期** `WEBHOOK_RETENTION_DAYS`（默认 **30 天**，
  比审计的 180 天短：这里是排障用途的大体积原文，非合规留痕）。启动、备份前与
  每日循环各清一次，过期投递连同其 attempts 在同一事务内删除。
  设为负数或非法值回退 30。

## 接收端点（仅限持登记证书的自建站点）

```
POST /api/hooks/{slug}
```

消费方仅限本 portal 管理的自建站点（hotspot 巡检/备份告警、workwork 备份/部署
通知等），调用方**必须持 portal 登记的有效客户端证书**：与其他业务路径一致套
`requireCert`（边缘 mTLS + 本地证书库双层，无免证路径）。端点可另配
per-endpoint secret 的 HMAC 签名作第二层加固（可选）。端点必须 `enabled=1`，
否则按不存在处理。

### 请求头

| 头 | 必需 | 说明 |
|---|---|---|
| `X-Webhook-Signature` | 是（端点已配 secret 时） | `sha256=<hex>`；缺前缀时按裸 hex 处理 |
| `X-Hub-Signature-256` | 兼容 | 仅当 `X-Webhook-Signature` 为空时取值，格式同 GitHub |
| `X-Webhook-Timestamp` | 否 | 参与签名时签名串为 `<ts>.` + body |
| `X-Webhook-Delivery` | 否 | 幂等键（去重） |
| `X-GitHub-Delivery` | 否 | 仅当 `X-Webhook-Delivery` 为空时取值 |

签名算法：`HMAC-SHA256(secret, payload)` 取 hex，与请求头常数时间比较。
`payload` = body；带 `X-Webhook-Timestamp` 时 payload = `"<ts>." + body`。

时间戳开启防重放：必须能解析为 Unix 秒且与当前时间偏移 ≤ **300s**，否则拒。
**注意**：该头是「二选一」开关而非可选增强——一旦携带，签名串就必须含 `ts`。

### 事件类型推断

`event_type` 依次取 `X-Webhook-Event` / `X-GitHub-Event` / `X-Event-Type` /
`X-Webhook-Topic` 头；都没有时从 JSON body 的 `type` 或 `event` 字段兜底，
仍取不到则为空串。

### 响应

| 状态码 | 场景 | 响应体 |
|---|---|---|
| `202` | 已受理（响应体 `dedup: false`） | `{"ok":true,"delivery_id":"wh-dlv-…","accepted":true,"dedup":false}` |
| `200` | 幂等命中（`dedup: true`，不新建记录、不重复转发） | `{"ok":true,"delivery_id":"<既有 id>","accepted":true,"dedup":true}` |
| `401` | 未持证书（`requireCert`：边缘 `X-Cert-Verify` 非 true） | `{"ok":false,"error":"client certificate required"}` |
| `401` | 签名缺失/不匹配/时间戳超窗 | `{"ok":false,"error":"invalid signature"}` |
| `403` | 证书未登记/已吊销，或来源非受信代理 | `{"ok":false,"error":"certificate not authorized"}` 等 |
| `404` | 端点不存在或 `enabled=0` | `{"ok":false,"error":"endpoint not found"}` |
| `405` | 非 POST | `{"ok":false,"error":"method not allowed"}` |
| `413` | body 超 256KB（`http.MaxBytesReader`，整条拒收**不落库**） | `{"ok":false,"error":"payload too large"}` |

签名失败会落一条 `status=rejected`、`reject_reason=signature mismatch` 的记录
（**body 原文仍落库**，便于排障），并写审计 `webhook.reject`。
受理成功写审计 `webhook.receive`。

### 未配置 secret 的端点

端点的 `secret` 为空时**不校验签名**（门禁已由 `requireCert` 兜底，持登记证书
方可到达），此层仅再要求请求来自受信代理（`TRUSTED_PROXY_CIDRS` /
`TRUSTED_PROXY_HOSTS`，见 `trustedproxy.go`），其余来源一律 `401`——纵深防御，
防「持有效证书的调用方向未配密钥的端点灌数据」。端点配 secret 即启用第二层
HMAC 校验（推荐对多来源端点使用）。

### body 上限

固定 **256KB**（`webhookMaxBody = 256 << 10`），当前**不是**可配置项。

## 管理 API

全部挂在 `/api/portal/webhooks/`，沿用证书门禁 + `requireRole`，响应统一
`{"ok":true,…}` / `{"ok":false,"error":…}` 信封。写操作均记审计（`webhook.*`）。

| 方法 | 路径 | 最低角色 | 说明 |
|---|---|---|---|
| GET | `/api/portal/webhooks/endpoints` | `ops.viewer` | 端点列表（`secret` 打码） |
| POST | `/api/portal/webhooks/endpoints/upsert` | `ops.admin` | body `{id?,name,slug,secret?,enabled,note}` |
| POST | `/api/portal/webhooks/endpoints/remove` | `ops.admin` | body `{id}`，级联删 sources |
| POST | `/api/portal/webhooks/endpoints/rotate-secret` | `ops.admin` | body `{id}` → `{"ok":true,"secret":"<新明文>"}` |
| GET | `/api/portal/webhooks/sources?endpoint_id=` | `ops.viewer` | 规则列表（`endpoint_id` 可选过滤） |
| POST | `/api/portal/webhooks/sources/upsert` | `ops.admin` | body `{id?,endpoint_id,name,target_url,secret?,enabled,max_attempts,timeout_ms}` |
| POST | `/api/portal/webhooks/sources/remove` | `ops.admin` | body `{id}` |
| GET | `/api/portal/webhooks/deliveries?limit=&status=&endpoint_id=` | `ops.viewer` | 列表，**不含 body 全文**；`limit` 缺省/越界归 100（上限 500） |
| GET | `/api/portal/webhooks/deliveries/get?id=` | `ops.viewer` | `{"delivery":{…,"body":…,"headers":{…}},"attempts":[…]}` |
| POST | `/api/portal/webhooks/deliveries/replay` | `ops.operator` | body `{id}`，状态归 `pending` 并清空 `reject_reason` |
| GET | `/api/portal/webhooks/stats` | `ops.viewer` | `{total,pending,delivered,failed,rejected,endpoints,sources}` |

约定：

- **密钥只（以明文）返回一次**：`rotate-secret` 的响应是唯一可读到新明文的机会；
  列表接口一律返回打码串（保留首尾各 4 位，中间定长 8 个 `*`，不泄露原长）。
  新建端点时 `secret` 由调用方在 `upsert` 里提供。
- `secret` 与 `slug` 都是**明文落库**，安全边界是证书门禁 + RBAC + 数据库文件权限，
  不是加密存储；`data/` 的备份视同敏感数据。
- `deliveries` 的 `status` 取值即上文五种；`limit` 非正整数返回 400
  （不同于数据层的缺省归 100，接口层先做校验）。
- `slug` 由服务端校验，规则为 `^[a-z0-9_-]{1,64}$`；不合法返回 400
  （含大写字母、空格、中文等一律拒）。
- `sources/upsert` 的 `endpoint_id` 必须真实存在，否则 400。

## 转发 worker

单 goroutine + `ticker`，默认每 **5s** 扫一遍 `status=pending` 的 deliveries，
按启用的 `sources` 逐条转发。

出站请求：`POST target_url`，**body 原样转发**，头：

- `Content-Type`：沿用入站记录的 `content_type`
- `X-Webhook-Delivery`：投递 id
- `X-Webhook-Event`：`event_type`
- `X-Webhook-Signature`：`sha256=<HMAC-SHA256(source.secret, body) hex>`

超时用该 source 的 `timeout_ms`（缺省/≤0 归 10000ms）。

### 退避与重试

第 `n` 次失败后的重试延迟按 `attempt_no` 取**固定表**（非指数计算）：
**5s / 30s / 2m / 10m / 30m**（超出档位一律取 30m，长尾不失控）。
`max_attempts` 缺省/≤0 归 3，且**上限截到 10**（防配成天文数字）。
每次尝试写一行 `webhook_attempts`。

### 转发的判定语义

- 非 2xx **视为失败**（下游明确拒收，重试有意义）；只有 2xx（`ok`）才算成功。
- 每个 tick 最多处理 **50** 条 `pending`（按 `received_at` 升序），单条失败
  不影响其它记录——worker 不能因一条坏数据停摆。
- **任一条规则本轮成功** → 整条投递即 `delivered`（不做「全部规则都成功」的强一致）。
- 全部规则都已超过重试上限 → `failed`（`reject_reason=超过最大重试次数`）；
  还有规则处在退避窗口内 → 保持 `pending`，下轮再试。
- **端点没有任何启用的规则**时，投递直接标 `delivered`（附
  `reject_reason=no enabled source`），不无限 pending——消息已接收并留存。
- 人工**重放**（`deliveries/replay`）会把状态归 `pending` 并**清空该投递的
  `webhook_attempts`**，即重放后从第 1 次尝试、无退避立即重来。
- 入站记录默认 `Content-Type` 为空时，出站回落 `application/json`；
  该投递的**响应体落库上限 4KB**（超出截断并加 `...(truncated)`）。

### SSRF 防线（安全红线，勿回退）

转发目标是管理员填的 URL，但同样会打到 portal 所在内网，故与拨测共用
`ssrfguard.go`：建连时刻以**真实目标 IP**判定（`probeDialControl` 挂在
`net.Dialer.Control`），DNS rebinding（解析公网、连接私网）与注册后改解析都拦得住；
不信任域名语义。私网目标默认拒绝。

`OPS_PROBE_ALLOW_PRIVATE=1` 会同时放开拨测**与** webhook 转发的私网限制
（两者共用 `probeAllowPrivate()`）——仅纯内网自托管联调可开，生产必须留空。

规则创建时（`sources/upsert`）`target_url` 另做静态校验（`validateWebhookTargetURL`）：

1. 必须 `http`/`https`；
2. 拒绝 userinfo（`http://user@host` —— 防混淆与凭据外泄）；
3. host 解析结果不得为环回/私网/链路本地。

任一不满足即 400 —— 给操作者即时反馈，与建连时校验**两层都要有**（静态校验
挡不住 DNS rebinding，故 dial control 不可省）。逃生阀沿用
`OPS_PROBE_ALLOW_PRIVATE`，与拨测**共用语义、不新造开关**。

## 管理台

`ui/src/routes/webhooks/+page.svelte`（导航「站点管理 → Webhook」）：

- 端点列表：接收 URL 复制、密钥展示（`SecretReveal`）、启停、轮换密钥；
- 转发规则管理：目标地址、出站 secret、`max_attempts` / `timeout_ms`；
- 投递记录：状态筛选表格 + 详情（请求头/body/逐次尝试）+ 重放按钮。

接收地址形如 `<origin>/api/hooks/<slug>`（`origin` 取浏览器当前 origin，
生产即 `https://ops.yinghuoren.com`）。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `WEBHOOK_DISPATCH_INTERVAL` | `5s` | 转发 worker 扫描 `pending` 投递的周期；解析失败或 < 1s 时回退 5s |
| `WEBHOOK_RETENTION_DAYS` | `30` | 投递与 attempts 的保留天数；启动、备份前与每日循环各清一次；非法/非正值回退 30 |
| `OPS_PROBE_ALLOW_PRIVATE` | 空（关闭） | 复用项：置非空同时放开拨测与 webhook 转发的私网目标限制 |

**除上面两项外没有其它 webhook 环境变量**：

- body 上限 256KB 是代码常量（`webhookMaxBody`）；
- `max_attempts`（默认 3）与 `timeout_ms`（默认 10000）是 **per-source 数据库字段**，
  在管理台按规则配置，不是全局环境变量；
- 退避档位 5s/30s/2m/10m/30m 是代码常量（`backoffForAttempt`）；
- 门户**自身告警**的出站渠道（`ALERT_WEBHOOK_URL` / `ALERT_WEBHOOK_SECRET`）属于
  告警引擎而非本模块（不经投递落库/重试/重放），见 `docs/portal-agent-protocol.md`
  「心跳超时告警」。

> ⚠️ 文档与代码一致性的核对方式：`grep -rn 'getEnv(' services/portal-backend/*.go`
> 是唯一权威——凡是代码没有 `getEnv` 读取的键，本节就不写。
> 设计阶段曾计划 `WEBHOOK_BODY_LIMIT_BYTES` / `WEBHOOK_MAX_ATTEMPTS`，最终**未落地**，
> 故不文档化（写进文档会导致「按文档配了却不生效」的静默误导）。

## 运维与排障

- **健康**：webhook 与 backend 同进程，`GET /api/portal/health` 即覆盖。
- **日志**：受理/拒收走 `log.Printf`（`portal: webhook received slug=… id=… bytes=… ip=…`），
  拒收另有 `portal: webhook reject record failed …`。
- **审计**：`webhook.receive` / `webhook.reject` / `webhook.*` 管理写操作，
  在「审计」页按 action 前缀筛选。
- **投递不通怎么查**：管理台投递详情看 `reject_reason` 与逐次 attempt 的
  `http_status` / `error` / `response_body`；`failed` 的改完目标后点重放即可重试。
- **调用方接不上**：先确认推送方持有效登记证书（无证在边缘即 403，到不了
  源站）；再确认端点 `enabled=1`、`slug` 与 URL 一致、
  签名串是否含时间戳（带了 `X-Webhook-Timestamp` 就必须按 `<ts>.` + body 签）。
