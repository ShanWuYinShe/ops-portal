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
- 认证：`Authorization: Bearer <agent-token>`；token 由 portal「Token 页」签发，
  一次性显示、可吊销，portal 只存 SHA-256 摘要。**一机一 token，泄露单吊即断。**
- 整条链路在 portal 的 mTLS 体系之后（边缘验证设备证书 + Origin Pulls），
  agent 侧无需客户端证书（token 即凭证），人访问 portal 才需要设备证书。

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

## 站点接入清单（onboarding）

1. 站点 docker-compose 化，服务有 healthcheck。
2. 被管机起 agent（见 `agent-compose.yml`）：配 `AGENT_ID`、portal 签发的
   `AGENT_TOKEN`、项目白名单、站点目录与 deploy 目录（同路径挂载）。
3. portal「Token 页」签发 token → 填入 agent env → 心跳出现在「站点页」。
4. **敏感操作子域名**（可选，站点自带管理界面时）：子域名加进
   `OPS_DOMAINS`（`pki/cf_edge_setup.py` 自动合并主机关联与透传规则），
   CF 主机关联启用 mTLS，源站同样接 X-Cert-* 头查表——
   保证所有站点的敏感操作只有持证用户可访问。
5. 备份：站点数据卷纳入备份 runbook。

## 版本演进

- 心跳/命令结构只加字段不改语义；portal 忽略未知字段，旧 agent 兼容。
- 破坏性变更升版本号（`ops.agent.v2`），portal 同期双兼容。
