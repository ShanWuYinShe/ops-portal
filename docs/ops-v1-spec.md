# ops.v1 — 被管网站 ops 管理接口约定

> 本文件是被管网站原生实现 `/ops/v1/*` 的**唯一事实源**：字段、鉴权、版本规则
> 以此处为准，portal 与各站点都按此实现。版本号 `v1`，演进只加字段不改语义。
> 边车 agent 模式的站点不需要实现本接口（由 agent 代答）。

## 地址约定

- 被管网站在自己域名下暴露 ops 接口，建议独立子域名
  `<site>-ops.yinghuoren.com`（敏感面与业务面隔离）。
- 子域名加入 `OPS_DOMAINS`（`pki/cf_edge_setup.py` 自动合并主机关联、
  证书透传与 WAF 规则），与主域名享受**同等全路径边缘 mTLS**。
- portal 注册表登记的是**服务根地址**（`ops_url`，如
  `https://hotspot-ops.yinghuoren.com`，末尾斜杠可选）；
  portal 拨测地址 = `{ops_url}/ops/v1/health`（由 portal 拼接，见「拨测」）。

## 鉴权（双向钉死）

- portal → 网站：portal 出示 **service 证书**（CF 托管 CA，kind=service）。
  网站**必须**校验 CF 透传头 `X-Cert-Serial`（大小写不敏感比较）
  等于 portal 下发的 service 证书 serial；其余一律 403。
  serial 在 portal「设备证书」页可查（kind=service），轮换时同步更新放行清单。
- 网站 → portal：网站凭 **site 注册证书** 调 `POST /api/site/register`
  自助登记 `ops_url`（见 `docs/agent-protocol.md`「统一网站视图」）。

参考实现：`services/opshttp/`（Go 标准库单包：鉴权中间件 + 两端点，
拷走即用，各站点不要重写验签）。

## 端点

### GET /ops/v1/health（存活）

```json
{"ok": true, "site": "hotspot", "time": "2026-09-14T12:00:00Z"}
```

- `site` = 注册名（与证书 label / compose 项目名一致）；
- 200 + `ok:true` 即存活；portal 拨测只认 2xx。

### GET /ops/v1/status（快照）

字段与 agent 心跳的容器/git 快照**同构**（portal 直接复用同一套视图类型）：

```json
{
  "ok": true,
  "site": "hotspot",
  "time": "2026-09-14T12:00:00Z",
  "containers": [
    {"name": "hotspot-nginx-1", "service": "nginx",
     "state": "running", "status": "Up 3 days (healthy)", "image": "hotspot-nginx"}
  ],
  "git": {"head": "88ac15d...", "branch": "main", "dirty": false}
}
```

- 字段全可选（缺失按未知展示），但 `service`/`state` 有则必须如实；
- `state` 取值建议与 `docker ps` 对齐（running/exited/restarting…）。

## 拨测（portal 侧）

- portal 后台每 `OPS_PROBE_INTERVAL`（默认 60s，最小 5s）以 service 证书
  拨测所有登记了 `ops_url` 的站点，结果进 `GET /api/portal/sites` 的
  `reachable`（true/false/nil=未探测）与 `checked_at` 字段，UI 展示徽标。
- service 证书未签发时整轮跳过；探测只记日志不记审计（防刷屏）。

## 注册前检查清单

1. 域名真实存在且边缘 mTLS 已覆盖（`OPS_DOMAINS` 生效）；
2. `/ops/v1/health` 本地直调 200，持 service 证书经公网 200、无证 403；
3. `ops_url` 登记服务根地址（不要带 `/ops/v1` 后缀，portal 自动拼接）；
4. portal「站点与服务」页 `reachable` 变绿后再宣布上线。

## 版本演进

- 只加字段不改语义；portal 忽略未知字段。
- 破坏性变更升版本号（`/ops/v2/*`），portal 同期双兼容。
