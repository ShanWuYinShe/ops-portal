# ops-portal（统一管理站 · 独立项目）

热点 `hotspot`、代理基建 `workwork`、静态站 `travel` 等所有站点的统一运维入口。
域名 `ops.yinghuoren.com`（CF 全代理 + 边缘 mTLS），与 `workwork` 平级、互不嵌套。

## 模型

边缘（Cloudflare）验人，源站认账；被管机跑 `ops-agent` 主动外连 portal，
portal 永不直连被管机。详见 `docs/cf-edge.md`。

**网站是一等实体**：agent 心跳的 compose 项目与原生注册的网站汇入同一视图
（`GET /api/portal/sites`），UI 一律按网站分组展示与操作，详见
`docs/agent-protocol.md` 的「统一网站视图」。

## 本地联调

```bash
cp .env.example .env && chmod 600 .env
bash pki/dev-selfsign.sh
docker compose --profile dev up -d --build
```

- 门禁：`https://localhost:8443/api/portal/whoami`（dev 自签证书，浏览器告警属预期；
  `.env` 里 `DEV_ALLOW_NO_CERT=1` 时免头，生产必须清空该值）
- 上报：`https://localhost:8443/api/portal/agents`（agent-local 每 5s 心跳一次）

生产部署：`ORIGIN_PULL_VERIFY=on` + 按 `docs/cf-edge.md` 配边缘规则。

## 目录

| 路径 | 说明 |
|---|---|
| `services/portal-nginx/` | 源站网关：Origin Pulls + X-Cert-* 透传 |
| `services/portal-backend/` | Go 门禁 + agent 心跳 + 前端托管 |
| `services/agent/` | Go 边车：docker 列表 + git 版本 + 心跳外连 |
| `ui/` | Svelte 5 空页（whoami + agents） |
| `docs/cf-edge.md` | CF 边缘配置清单 |
| `pki/` | dev 自签脚本（永不用于生产） |
