# ops-portal（统一管理站 · 独立项目）

热点 `hotspot`、代理基建 `workwork`、静态站 `travel` 等所有站点的统一运维入口。
域名 `ops.yinghuoren.com`（CF 全代理 + 边缘 mTLS），与 `workwork` 平级、互不嵌套。

## 模型

边缘（Cloudflare）验人，源站认账；被管机跑 `portal-agent` 主动外连 portal，
portal 永不直连被管机。详见 `docs/cf-edge.md`。

**网站是一等实体**：portal-agent 心跳的 compose 项目与原生注册的网站汇入同一视图
（`GET /api/portal/sites`），UI 一律按网站分组展示与操作，详见
`docs/portal-agent-protocol.md` 的「统一网站视图」。

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
统一走部署脚本（版本注入＋构建＋健康校验＋回滚提示）：

```bash
./scripts/deploy.sh                # 本地/默认栈
DEPLOY_ENV=prod ./scripts/deploy.sh  # 生产（叠加 docker-compose.prod.yml）
```

`UI_VERSION`/`IMAGE_TAG` 均由脚本按当前 git 自动注入；`UI_VERSION` 显示在
侧栏底部 `build <sha>`，用于核对线上实际运行的版本；回滚见脚本输出。

生产前置（`docker-compose.prod.yml`，缺一项 `up` 即失败）：

- `.env` 按 `.env.prod.example` 配置；
- `cert/` 放 Cloudflare Origin CA 源站证书 `server_cert.pem` / `server_cert.key`；
- 外部网络 `ipvlan_docker` 已存在（ipvlan L3 公网 IPv6 直连；宿主 443 归
  workwork xray-forward，portal 不再发布宿主端口）。

## 测试

单测/静态检查由 CI 门禁（Go 三 module + ui + smoke + e2e）；本地复现 e2e：

```bash
docker build -t ops-portal-backend:e2e -f services/portal-backend/Dockerfile .
docker run -d --name e2e -p 127.0.0.1:8319:8319 -e DATA_DIR=/tmp/data \
  -e DEV_ALLOW_NO_CERT=1 -e TRUSTED_PROXY_CIDRS=0.0.0.0/0 ops-portal-backend:e2e
cd e2e && npm ci && npx playwright install chromium && npx playwright test
docker rm -f e2e
```

## 目录

| 路径 | 说明 |
|---|---|
| `services/portal-nginx/` | 源站网关：Origin Pulls + X-Cert-* 透传 |
| `services/portal-backend/` | Go 门禁 + portal-agent 心跳 + 前端托管 |
| `services/portal-agent/` | Go 边车：docker 列表 + git 版本 + 心跳外连 |
| `ui/` | Svelte 5 管理台（仪表盘 / 站点与服务 / 命令与发布 / 用户与设备 / 机器凭据 / 审计） |
| `docs/cf-edge.md` | CF 边缘配置清单 |
| `pki/` | dev 自签脚本（永不用于生产） |
