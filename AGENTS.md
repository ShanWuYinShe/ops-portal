# ops-portal

统一运维门户（域名 `ops.yinghuoren.com`，Cloudflare 边缘 mTLS 回源）。

通用约束（Git 规范、Commit 格式、验证流程、Docker 优先等）以全局 `~/AGENTS.md` 为准，本文件只做项目特有补充。

## 仓库结构

- `services/portal-backend/` — Go 主后端（独立 go.mod + Dockerfile）
- `services/portal-agent/` — Go 主机代理（独立 go.mod + Dockerfile；协议见 `docs/portal-agent-protocol.md`）
- `services/opshttp/` — Go 参考包（无 main；发给被管站点「拷走即用」，backend/agent 不 import）
- `services/portal-nginx/` — nginx 配置
- `ui/` — Svelte 5 + SvelteKit + Tailwind 4，包管理器 **bun**（bun.lock 唯一权威）
- `e2e/` — Playwright 端到端测试，包管理器 npm（package-lock.json 唯一权威，与 ui/ 的 bun 并存，勿混用）
- `pki/` — 仅开发自签证书，**永不用于生产**
- compose 三份：`docker-compose.yml`（开发）/ `docker-compose.prod.yml`（生产）/ `portal-agent-compose.yml`（agent 独立编排）；部署脚本 `scripts/deploy.sh`

## 构建与验证

本地只跑秒级静态检查（命令见下节「CI 门禁与本地验证分工」）；全量单测、镜像构建、容器冒烟、E2E 一律交 CI，本地不跑。
定位问题时按需使用：

```bash
# 各 Go 服务目录内分别执行（独立 module，不共享 go.mod）
go test ./...

# ui（bun，检查命令是 svelte-check）
cd ui && bun run test
```

- 环境变量模板：`.env.example` / `.env.prod.example`（真实 `.env` 不入库）。
- 边缘/数据库/协议细节见 `docs/`（cf-edge.md、database.md、ops-v1-spec.md、portal-agent-protocol.md）。
- 告警 webhook 网关（签名校验、mTLS 调用方证书、去重与限流）：详见 skill `ops-portal-webhook-gateway`。

## CI 门禁与本地验证分工

重活（全量单测、镜像构建、容器冒烟、E2E、compose 校验、gitleaks）一律交给 GitHub Actions，本地只跑秒级检查。
完整流程见全局 skill `github-ci-flow`：开发分支推送 → `gh pr create` → `gh pr checks --watch` 全绿 →
`gh pr merge --rebase --delete-branch` → 本地 `git pull --ff-only` 并清理 worktree。

- 本地（提交前，秒级）：
  - `gofmt -l services/portal-backend/ services/portal-agent/ services/opshttp/`（应无输出，实测约 0.03s）
  - 三个 Go module 各自 `go vet ./...` 与 `go build ./...`（实测 backend 约 1–7s、agent 约 0.5–2s、opshttp 亚秒，取决于编译缓存）
  - `cd ui && bun run check`（svelte-kit sync + svelte-check，实测约 3s）
  - `shellcheck scripts/*.sh pki/*.sh`（实测亚秒）
- CI 覆盖（`.github/workflows/ci.yml`，push main 与 PR 均触发）：
  - `backend`（Portal Backend (Go)）：gofmt / go vet / staticcheck / go build / `go test -race` / govulncheck（阻断）
  - `agent`（Portal Agent (Go)）：gofmt / go vet / staticcheck / go build / `go test -race` / govulncheck（非阻断；已知接受项与原因见 `ci.yml` 该 job 的注释）
  - `opshttp`（opshttp (reference package)）：gofmt / go vet / staticcheck / go build / `go test -race` / govulncheck（阻断）
  - `ui`（Portal UI (SvelteKit)）：`bun install --frozen-lockfile` / svelte-check / vitest / build
  - `smoke`（Container smoke (backend image)）：docker build backend 镜像 → 起容器 → health、404/405、SPA 回退、CSP 契约断言
  - `e2e`（UI E2E (Playwright)）：docker build backend → 起容器 → Playwright chromium 跑浏览器级 E2E
  - `images`（Container build & smoke (nginx & agent)）：nginx、agent 镜像真实构建 + 最小启动冒烟
  - `repo-lint`（Repo lint (compose config & shellcheck)）：三套 compose `config -q` + shellcheck
  - `gitleaks`（Secret scan (gitleaks)）：全量历史秘密扫描
- CI 覆盖不到、必须本地/真机验证的：无。容器镜像构建、容器冒烟、Playwright E2E 全部由 CI 覆盖，本地不做 `docker build`（仅交互式调试时启动容器）；`README.md`「本地复现 e2e」仅排障用，不是提交前门禁。
- 发版/发布门禁不变：部署按 `scripts/deploy.sh` 执行（`DEPLOY_ENV=prod` 先跑生产预检；镜像 tag 与 UI 版本同源 git，回滚 = 旧 tag `up -d`），属发布动作、非日常验证门禁。
