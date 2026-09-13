# ops.agent.v1 — 站点统一接入协议

> 任何网站（docker-compose 站点或自实现后端）实现本协议即可接入 ops-portal，
> 获得统一的健康监控、容器操作、发布与审计能力。版本号 `v1`，演进只加不改。

## 接入方式（二选一）

| 方式 | 适用 | 成本 |
|---|---|---|
| **A. 边车 agent（推荐）** | 任何 docker-compose 站点 | 零代码改动，起一个容器 |
| **B. 原生实现** | 想深度集成（上报业务指标、应用级健康）的站点 | 后端实现两个 HTTP 端点 |

参考实现：`services/agent/`（Go，方式 A）；portal 后端 `services/portal-backend/`。

## 传输与安全

- 方向：**agent 主动外连 portal**（被管机零入站端口，NAT/CF 后无感知）。
- URL：`https://ops.yinghuoren.com/api/agent/*`（生产）或内网直连（同机联调）。
- 认证：**双因子**——机器客户端证书（CF 托管 CA，kind=agent，过边缘 mTLS）
  + `Authorization: Bearer <agent-token>`。portal「Agent 令牌」页签发
  「机器证书包」（agent_token/client_cert/client_key 一次下发），机器证书
  在证书表登记并与 agent_id 绑定；同宿主机内网直连仅 token（网络隔离）。
- 边缘 WAF 对 ops 域名**全部路径**强制 mTLS（含 /api/agent/*，机器也持证，
  不留免证路径）；token 泄露单独无效，必须连同机器证书才可通过边缘。
边缘 WAF 规则对 `/api/agent/*` 路径豁免 mTLS（agent 有独立 token 鉴权），
其余路径无证书一律边缘拦截。

## 证书分类（严格分域，互不混用）

| kind | 签发给 | 用途 | 能访问 |
|---|---|---|---|
| `human` | 人（每台设备） | 登录 portal 管理面 | `/api/portal/*` 全部（按 RBAC） |
| `agent` | 被管机 ops-agent | 机器上报与命令执行 | `/api/agent/*` |
| `service` | **ops 服务端自身** | portal 主动调用被管网站的 ops 接口时出示 | 被管网站的 `/ops/v1/*`（各网站放行清单钉死其 serial） |
| `site` | 被管网站 | 网站访问 ops 注册 API 自助登记 | `/api/site/register` |

human 与 service/site/agent 物理同源（CF 托管 CA）但 kind 隔离、权限面互斥：
管理面只认 human，机器树只认 agent，注册树只认 site，被管网站只认 service。

## 被管网站的 ops 管理接口（/ops/v1/*，原生实现模式）

网站原生实现统一被管接口时，在自己域名下暴露（建议 `<site>-ops.yinghuoren.com`，
加入 OPS_DOMAINS 后同样全路径强制边缘 mTLS）：

- **放行规则**：仅接受出示 `service` 证书的调用方（校验透传的
  `X-Cert-Serial` 等于 portal service 证书 serial；其余一律 403）。
- 端点（与 ops.agent.v1 同面，方向相反，由 portal 拉取）：
  `GET /ops/v1/health`、`GET /ops/v1/status`（站点/容器/版本快照）。
- 接入：portal 签发 `site` 注册证书 → 网站调 `POST /api/site/register`
  登记自己的 ops 接口地址 → portal 以 service 证书调用验证连通。

## 注册（enrollment，免人工复制 token）

portal「Agent 令牌」页签发**一次性注册令牌**（`enroll_` 前缀，绑定 agent_id、
30 分钟有效、单次使用）→ 被管机自助换取正式 token：

```
POST /api/agent/enroll
{"enroll_token": "enroll_...", "agent_id": "agent-xxx-01"}
→ {"ok": true, "token": "opsa_..."}
```

agent 亦支持启动时自注册：设 `AGENT_ENROLL_TOKEN`（留空 `AGENT_TOKEN`），
首次启动自动完成上述交换。一键接入：`scripts/onboard-agent.sh`（在被管机的
仓库 checkout 内执行，生成 compose、注册、构建、启动一步完成）。

## 端点

### POST /api/agent/heartbeat

agent 每 10s（可配 `HEARTBEAT_INTERVAL`）上报快照，响应体携带待执行命令。

请求体：

```json
{
  "agent_id": "agent-server-01",
  "hostname": "vmi3222623",
  "compose_projects": ["workwork", "hotspot"],
  "sites": [
    {"name": "workwork", "path": "/home/workuser/workwork",
     "head": "88ac15d...", "branch": "main", "dirty": false}
  ],
  "containers": [
    {"id": "613e1f9cb2d4", "name": "workwork-nginx-1", "project": "workwork",
     "service": "nginx", "state": "running", "status": "Up 3 days (healthy)",
     "image": "workwork-nginx"}
  ]
}
```

响应体（portal 下发命令队列，可为空数组）：

```json
{"ok": true, "commands": [
  {"id": "cmd-...", "agent_id": "agent-server-01",
   "action": "restart", "service": "nginx", "project": "", "targets": null}
]}
```

### POST /api/agent/report

命令执行结果回传（逐条立即回传；目标为 agent 自身时先回传再执行）：

```json
{"agent_id": "agent-server-01", "reports": [
  {"id": "cmd-...", "status": "done", "result": "restart ok"}
]}
```

`status` ∈ `done | failed`；`result` 为人可读的执行输出（截断到 8KB）。

## 命令

| action | 参数 | 语义 |
|---|---|---|
| `start` / `stop` / `restart` | `service` | 按 compose service 名操作容器（项目白名单内） |
| `deploy` | `project` + `targets[]` | `docker compose -p <project> --project-directory <dir> up -d --build <targets...>`，只重建受影响服务 |

命令生命周期：`queued → acked（agent 取走）→ done | failed`。
agent 侧双重白名单：`AGENT_COMPOSE_PROJECTS`（项目）+ `AGENT_DEPLOY_DIRS`（目录映射）。

## deploy 路径语义（关键，踩过坑）

`AGENT_DEPLOY_DIRS` 配**宿主机绝对路径**，且 agent 容器须以**同路径只读挂载**
（源 == 目标，如 `- /srv/hotspot:/srv/hotspot:ro`）。原因：compose CLI 在 agent
容器内执行，`-f` 与相对卷按容器内路径解析后再交 daemon；同路径挂载保证两侧一致。

## 接入清单（onboarding）

### 新机器（首次）

1. 机器上 checkout ops-portal 仓库（远程仓库建好后 `git clone` 即可）。
2. portal「Agent 令牌」页签发注册令牌（填 agent_id，如 `agent-<机器名>-01`）。
3. 跑 `scripts/onboard-agent.sh`（参数：--portal --agent-id --enroll-token
   --project/--site/--deploy 可多次，--local 仅限与 portal 同宿主机）。
4. portal「站点与服务」页确认心跳出现。完成。

### 已接入机器上新增站点

1. 站点 docker-compose 化，服务有 healthcheck。
2. 编辑该机 `agent-compose.generated.yml`：项目白名单加项目名、
   AGENT_SITES/AGENT_DEPLOY_DIRS 加 `project:/宿主机绝对路径`、
   volumes 加同路径只读挂载 → `docker compose up -d`。
3. portal「站点」页 10 秒内出现新容器。完成。

### 敏感操作子域名（站点自带管理界面时，可选）

子域名加进 `OPS_DOMAINS`（`pki/cf_edge_setup.py` 自动合并主机关联、
透传与 WAF 规则），源站同样接 X-Cert-* 头查表——保证所有站点的敏感操作
只有持证用户可访问。执行须在 portal 服务器上（token 有 IP 白名单）。

### 其他

- 备份：站点数据卷纳入备份 runbook。

## 版本演进

- 心跳/命令结构只加字段不改语义；portal 忽略未知字段，旧 agent 兼容。
- 破坏性变更升版本号（`ops.agent.v2`），portal 同期双兼容。
