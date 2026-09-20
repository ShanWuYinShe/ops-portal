#!/usr/bin/env bash
# ── ops-portal 部署脚本 ──────────────────────────────────────────
# 版本注入 + 构建 + 更新 + 健康校验 + 精确回滚提示。
#
# 用法（仓库根目录）：
#   ./scripts/deploy.sh                # 本地/默认：docker-compose.yml
#   DEPLOY_ENV=prod ./scripts/deploy.sh  # 生产：叠加 docker-compose.prod.yml
#   （prod 要求 ipvlan_docker 外部网络与 cert/ 已就绪，见 README）
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# ── 工作区校验：脏树 / 落后远端一律中止 ─────────────────────────
# 部署产物（镜像 tag、UI 版本指纹）都源自当前 HEAD，脏树会造出来路不明的
# 版本；落后远端则会把旧代码当"最新"发上去。两种情况都宁可停下来人工确认。
if [ -n "$(git status --porcelain)" ]; then
  echo "!! 工作区有未提交改动，中止部署（先 commit / restore 再跑）：" >&2
  git status --short >&2
  exit 1
fi

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
# 注意：下面 echo 里的中文全角括号紧贴变量时必须用 ${VAR} 隔开，
# 否则 bash 会把多字节字符并入变量名（set -u 下报 unbound variable）。
echo "==> 核对远端状态（origin/${BRANCH}）…"
git fetch --quiet origin "$BRANCH" || {
  echo "!! git fetch origin $BRANCH 失败（网络或权限？），无法确认是否落后远端，中止部署。" >&2
  echo "   确认网络后重跑；若确要离线部署，请人工核对本地与远端一致后自行承担。" >&2
  exit 1
}
BEHIND="$(git rev-list --count HEAD..origin/"$BRANCH")"
if [ "$BEHIND" -ne 0 ]; then
  echo "!! 本地 $BRANCH 落后 origin/${BRANCH} ${BEHIND} 个提交，中止部署。" >&2
  echo "   先 git pull --ff-only 同步后再跑（不要在落后状态下发布旧代码）。" >&2
  exit 1
fi

DEPLOY_ENV="${DEPLOY_ENV:-local}"
case "$DEPLOY_ENV" in
  local) COMPOSE="docker compose -f docker-compose.yml" ;;
  prod)  COMPOSE="docker compose -f docker-compose.yml -f docker-compose.prod.yml" ;;
  *) echo "!! DEPLOY_ENV 仅支持 local|prod（当前值：${DEPLOY_ENV}）" >&2; exit 1 ;;
esac

# 不可变版本：镜像 tag 与前端构建标识同源（git），回滚 = 旧 tag up -d。
export UI_VERSION="${UI_VERSION:-$(git rev-parse --short HEAD)}"
export IMAGE_TAG="${IMAGE_TAG:-$(git describe --always --tags --dirty 2>/dev/null || echo dev)}"
echo "==> 版本: UI_VERSION=$UI_VERSION IMAGE_TAG=$IMAGE_TAG"

echo "==> 构建镜像…"
UI_VERSION="$UI_VERSION" IMAGE_TAG="$IMAGE_TAG" $COMPOSE build portal-backend portal-nginx

echo "==> 更新容器…"
UI_VERSION="$UI_VERSION" IMAGE_TAG="$IMAGE_TAG" $COMPOSE up -d portal-backend portal-nginx

echo "==> 健康校验…"
fail=""
for svc in portal-backend portal-nginx; do
  ok=""
  for _ in $(seq 1 30); do
    state="$(docker inspect "$($COMPOSE ps -q "$svc")" --format '{{.State.Health.Status}}' 2>/dev/null || echo none)"
    if [ "$state" = "healthy" ]; then ok=1; break; fi
    sleep 5
  done
  if [ -z "$ok" ]; then echo "!! $svc 150s 内未 healthy"; fail="$fail $svc"; fi
done

# 版本指纹：侧栏底部 build <sha> 的数据源头即 UI_VERSION。
$COMPOSE exec -T portal-backend wget -qO- http://localhost:8319/api/portal/health 2>/dev/null | head -c 300 || true; echo
if [ -n "$fail" ]; then
  echo "!! 部署校验未通过:$fail"
  echo "回滚：IMAGE_TAG=<旧tag> $COMPOSE up -d portal-backend portal-nginx"
  echo "（旧 tag 镜像须仍在本地：勿对它们 docker image prune）"
  exit 1
fi
echo "🎉 部署完成（回滚：IMAGE_TAG=<旧tag> $COMPOSE up -d portal-backend portal-nginx）"
