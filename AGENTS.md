# ops-portal

统一运维门户（域名 `ops.yinghuoren.com`，Cloudflare 边缘 mTLS 回源）。

通用约束（Git 规范、Commit 格式、验证流程、Docker 优先等）以全局 `~/AGENTS.md` 为准，本文件只做项目特有补充。

## 仓库结构

- `services/portal-backend/` — Go 主后端（独立 go.mod + Dockerfile）
- `services/portal-agent/` — Go 主机代理（独立 go.mod + Dockerfile；协议见 `docs/portal-agent-protocol.md`）
- `services/opshttp/` — Go 参考包（无 main；发给被管站点「拷走即用」，backend/agent 不 import）
- `services/portal-nginx/` — nginx 配置
- `ui/` — Svelte 5 + SvelteKit + Tailwind 4，包管理器 **bun**（bun.lock 唯一权威）
- `e2e/` — Playwright 端到端测试
- `pki/` — 仅开发自签证书，**永不用于生产**
- compose 三份：`docker-compose.yml`（开发）/ `docker-compose.prod.yml`（生产）/ `portal-agent-compose.yml`（agent 独立编排）；部署脚本 `scripts/deploy.sh`

## 构建与验证

```bash
# 各 Go 服务目录内分别执行（独立 module，不共享 go.mod）
go build ./... && go vet ./... && go test ./...

# ui（bun，检查命令是 svelte-check）
cd ui && bun install && bun run check
```

- 环境变量模板：`.env.example` / `.env.prod.example`（真实 `.env` 不入库）。
- 边缘/数据库/协议细节见 `docs/`（cf-edge.md、database.md、ops-v1-spec.md、portal-agent-protocol.md）。
