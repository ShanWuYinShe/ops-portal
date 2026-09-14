# ops.v1 — 被管网站 ops 管理接口约定

> 本文件是被管网站原生实现 `/ops/v1/*` 的**唯一事实源**：端点、字段、语义、
> 鉴权、版本规则以此处为准，portal 与各站点都按此实现。版本号 `v1`，
> 演进只加字段不改语义。
>
> 设计原则：**只让网站实现"只有它自己知道"的东西。**
> 容器状态、镜像、git 版本是宿主机层面的事实，由 agent 边车上报；
> 业务健康（DB 通不通、redis 活不活、迁移跑完没）只有应用进程自己知道，
> 这才是原生接口存在的理由。任何 agent 已上报的信息，网站不得重复实现。

## 接口：只有一个

### GET /ops/v1/health（存活 + 就绪，唯一的强制接口）

```json
{
  "ok": true,
  "site": "hotspot",
  "status": "ok",
  "time": "2026-09-14T12:00:00Z",
  "version": "1.2.3",
  "commit": "88ac15d",
  "checks": [
    {"name": "postgres", "ok": true, "latency_ms": 3},
    {"name": "redis",    "ok": false, "detail": "timeout after 2s"}
  ]
}
```

| 字段 | 必需 | 含义 |
|---|---|---|
| `ok` | 是 | 进程能否对外服务。`false` 时 portal 无条件视为故障，不看 `status` |
| `site` | 是 | 注册名（与证书 label / compose 项目名一致） |
| `status` | 是 | 站点**自己判定**的健康档：`ok` \| `degraded` \| `starting`；除 `ok` 外 portal 一律亮灯 |
| `time` | 是 | 服务端时间（RFC3339，portal 顺带发现时钟漂移） |
| `version` / `commit` | 否 | 应用构建版本号/commit（agent 报的是仓库状态，两者不等价） |
| `checks` | 否 | 依赖检查明细；`latency_ms` / `detail` 可选 |

状态语义（portal 的展示与之严格对应）：

| `status` | 含义 | 操作员动作 |
|---|---|---|
| `ok` | 全部正常 | 无 |
| `degraded` | 活着但有依赖故障 | 按 `checks` 定位，不需要等 |
| `starting` | 活着但尚未就绪（迁移中、预热中） | 等，不需要查 |

`ok:false` 独立于 `status`：进程能应答但声明自己不可服务时使用
（如主动摘流量）。portal 收到即按故障处理。

## 方法：只接受 GET

`POST /ops/v1/health` 返回 **405**，不做兼容、不接受双动词。

原因（一次性说清，不再讨论）：健康检查的消费者是机器——负载均衡、
拨测、curl、浏览器，以及未来一切通用监控工具，它们全部默认 GET。
一个只能 POST 的健康端点，对这些工具等于不存在。
hotspot 的 POST-only 约定管的是它的**业务 API**（防缓存、统一 SDK），
ops 子树是跑在独立子域名上的**基础设施契约**，消费者和目的都不同，
不受该约定约束；chi 里多挂一行 `r.Get` 即合规，零成本。

## 延迟规则（硬性）

1. 端到端响应 **≤ 1s**。portal 拨测超时 10s，但 1s 是给站点定的上限：
   健康端点慢，等于健康系统本身不健康。
2. 每个依赖检查自带超时（**≤ 2s**），超时记为该项 `ok:false`，
   不得阻塞整个响应。
3. 检查失败永远表达为 `checks` 条目，**不得返回 500**。
   非 2xx 只保留给一种情况：进程自身已无法应答
   （此时 portal 本来也连不上，状态码是什么已不重要）。
4. 建议（SHOULD）：检查结果缓存 5–10s。portal 每 60s 才拨测一次，
   每次现查 DB 属于浪费。

## 明确不做的（v1 范围外，勿自行发挥）

- **容器/镜像/git 上报**：agent 的职责。业务进程通常没有 docker.sock，
  也不该有；重复上报只会在统一视图里制造冲突。
- **请求体**：GET 无 body，参数为零。
- **业务信封**（如 hotspot 的 `{code,message,data}`）：那是业务 API 的契约，
  ops 面用 HTTP 状态 + 本契约字段表达一切，不套娃。
- **业务鉴权**（用户 token/会话）：ops 面只认 service 证书，见下。
- **应用级动作**（清缓存、重建索引、优雅 reload）与**业务指标**：
  等出现第二个真实需求时再标准化（Prometheus exposition format 优先，
  不自创 JSON 指标 schema）。v1 不预留、不占位。

## 鉴权（双向钉死，不变）

- portal → 网站：portal 出示 **service 证书**（CF 托管 CA，kind=service）。
  网站**必须**校验 CF 透传头 `X-Cert-Serial`（大小写不敏感比较）
  等于 portal 下发的 service 证书 serial；其余一律 403，
  配置缺失（serial 为空）时必须拒绝一切请求（fail-closed）。
  serial 在 portal「设备证书」页可查（kind=service），轮换时同步更新放行清单。
- 网站 → portal：网站凭 **site 注册证书** 调 `POST /api/site/register`
  自助登记 `ops_url`（见 `docs/agent-protocol.md`「统一网站视图」）。

参考实现：`services/opshttp/`（Go 标准库单文件：鉴权中间件 + 健康端点
+ 超时/panic 兜底，拷走即用，各站点不要重写验签）。

## 地址约定（不变）

- 独立子域名 `<site>-ops.yinghuoren.com`（敏感面与业务面隔离）。
- 子域名加入 `OPS_DOMAINS`（`pki/cf_edge_setup.py` 自动合并主机关联、
  证书透传与 WAF 规则），与主域名享受**同等全路径边缘 mTLS**。
- portal 注册表登记**服务根地址**（`ops_url`，末尾斜杠可选）；
  portal 拨测地址 = `{ops_url}/ops/v1/health`（由 portal 拼接）。

## 拨测（portal 侧）

- portal 后台每 `OPS_PROBE_INTERVAL`（默认 60s，最小 5s）以 service 证书
  拨测所有登记了 `ops_url` 的站点。
- 判定：非 2xx → 不可达；2xx → 可达，且 `status`/`checks` 原样进视图：
  `ok` 绿灯，`degraded`/`starting` 黄灯并列出故障项，`ok:false` 红灯。
- service 证书未签发时整轮跳过；探测只记日志不记审计（防刷屏）。

## 静态站与 agent 站点

- **纯静态站（如 travel）实现零接口**：没有进程、没有依赖，无可报告。
  其版本信息由 agent 的 git 上报覆盖；CDN/源站可用性不属于 ops 契约
  （那是被动 URL 监控工具的职责，不在本契约内）。
- **已有 agent 边车的 compose 站点**：容器/git 已覆盖，实现本接口是
  **可选**的；只有当你需要"降级可视"（DB 挂了但容器还在跑）时才值得做。

## 注册前检查清单

1. 域名真实存在且边缘 mTLS 已覆盖（`OPS_DOMAINS` 生效）；
2. 无证 403，有效 service 证书 200；响应 ≤ 1s（含最慢依赖超时）；
3. 杀掉一个依赖后返回 `200 + status:"degraded"` 且故障项在 `checks` 里；
4. `ops_url` 登记服务根地址（不要带 `/ops/v1` 后缀，portal 自动拼接）；
5. portal「站点与服务」页徽标符合预期后再宣布上线。

## 版本演进

- 只加字段不改语义；portal 忽略未知字段；站点忽略未知请求头。
- 破坏性变更升版本号（`/ops/v2/*`），portal 同期双兼容。
- 历史：v1 曾定义 `/ops/v1/status`（容器/git 快照），因与 agent 职责重复
  且站点物理上无法提供（无 docker.sock），已删除；无任何消费者受影响。
