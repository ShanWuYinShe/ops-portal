#!/usr/bin/env bash
# ── ops-portal 部署脚本 ──────────────────────────────────────────
# 版本注入 + 构建 + 更新 + 健康校验 + 精确回滚提示。
#
# 用法（仓库根目录）：
#   ./scripts/deploy.sh                # 本地/默认：docker-compose.yml
#   DEPLOY_ENV=prod ./scripts/deploy.sh  # 生产：叠加 docker-compose.prod.yml
#   （prod 先跑预检：ipvlan_docker 网络 / cert 证书 / .env 的 DEV_ALLOW_NO_CERT /
#   data 属主，任一不满足即中止并打印修复指引，清单见 README「生产前置」）
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

# ── prod 预检：全新生产部署首次启动路径的前置条件，任一失败即中止 ──
# 仅生产模式执行，local/dev 不检查（避免影响本地联调）。
# 累计列出全部失败项再退出，一次跑完就能逐项修复。
if [ "$DEPLOY_ENV" = "prod" ]; then
  echo "==> prod 预检…"
  precheck_failed=""

  pre_fail() {
    precheck_failed=1
    echo "!! $1" >&2
  }

  # 1) 外部网络 ipvlan_docker：docker-compose.prod.yml 声明 external，名字固定。
  #    portal 生产经 CF 回源、经该网络 ipvlan L3 直连公网 IPv6（不发布宿主端口）。
  if ! docker network inspect ipvlan_docker >/dev/null 2>&1; then
    pre_fail "外部网络 ipvlan_docker 不存在（或 docker 不可达）。"
    echo "   修复：在宿主创建 ipvlan L3 网络（与 workwork 同款：--ipv6 + 公网 IPv6" >&2
    echo "   子网/网关 + -o parent=物理网卡），创建后 docker network inspect ipvlan_docker" >&2
    echo "   确认存在再重跑；清单见 README「生产前置」。" >&2
  fi

  # 2) 源站证书：compose 挂 ./cert → /etc/nginx/certs，nginx 按固定名加载。
  for cert_file in cert/server_cert.pem cert/server_cert.key; do
    if [ ! -r "$cert_file" ]; then
      pre_fail "缺少源站证书 ${cert_file}。"
      echo "   修复：Cloudflare Origin CA 证书与私钥放到 cert/server_cert.pem 与" >&2
      echo "   cert/server_cert.key（私钥 chmod 600），来源见 docs/cf-edge.md。" >&2
    fi
  done

  # 3) DEV_ALLOW_NO_CERT 必须为空：该开关打开即无证请求按 admin 放行，
  #    是认证链单点总闸。compose 的变量替换源 = shell 环境与仓库根 .env，
  #    shell 侧会覆盖 .env，两处都查。
  if [ ! -f .env ]; then
    pre_fail "缺 .env（prod 的 TRUSTED_PROXY/透传头/CF token 等全走它）。"
    echo "   修复：cp .env.prod.example .env && chmod 600 .env，填齐真实值。" >&2
  else
    env_nocert="$(grep -E '^[[:space:]]*(export[[:space:]]+)?DEV_ALLOW_NO_CERT=' .env \
      | tail -n 1 | cut -d= -f2- | tr -d '\r"[:space:]' || true)"
    if [ -n "${DEV_ALLOW_NO_CERT:-}" ] || [ -n "$env_nocert" ]; then
      pre_fail "DEV_ALLOW_NO_CERT 非空（shell=${DEV_ALLOW_NO_CERT:-<未设>}，.env=${env_nocert:-<空>}）。该开关打开即无证请求按 admin 放行，是认证链单点总闸，禁止开着上生产。"
      echo "   修复：.env 中置 DEV_ALLOW_NO_CERT=（留空），并 unset 当前 shell 的" >&2
      echo "   DEV_ALLOW_NO_CERT 后重跑。" >&2
    fi
  fi

  # 4) ./data 对 uid 1001 可写：backend 容器以 1001:1001 运行（./data:/app/data）。
  #    首次 up 时目录若由 docker(root) 代建或属主不对，backend MkdirAll 失败
  #    → crash loop。写权限按 owner/group/other 写位判断（兼容 GNU/BSD stat）。
  if [ ! -d data ]; then
    pre_fail "data/ 目录不存在（docker 会以 root 代建，backend(uid 1001) 无写权限 → crash loop）。"
    echo "   修复：sudo install -d -o 1001 -g 1001 data" >&2
  else
    perms="$(stat -c '%u %g %a' data 2>/dev/null || stat -f '%u %g %Lp' data)"
    d_uid="${perms%% *}"; d_rest="${perms#* }"
    d_gid="${d_rest%% *}"; d_mode="${d_rest#* }"
    d_triad="${d_mode: -3}"
    d_writable=0
    if [ "$d_uid" = "1001" ]; then case "$d_triad" in [2367]??) d_writable=1 ;; esac; fi
    if [ "$d_gid" = "1001" ]; then case "$d_triad" in ?[2367]?) d_writable=1 ;; esac; fi
    case "$d_triad" in ??[2367]) d_writable=1 ;; esac

    if [ "$d_writable" -eq 1 ]; then
      echo "    data/ uid=${d_uid} gid=${d_gid} 权限=${d_mode}（uid 1001 可写）"
    else
      pre_fail "data/ 对 uid 1001 不可写（uid=${d_uid} gid=${d_gid} 权限=${d_mode}），backend 首启 MkdirAll 失败 → crash loop。"
      echo "   修复：sudo chown -R 1001:1001 data" >&2
    fi
  fi

  # 5) legacy 共享 agent token：无 agent 身份绑定（持 token 者可冒充任意
  #    agent 收命令/回传结果），双因子退化为单因子。生产必须迁移为
  #    agent-tokens 逐机签发；确要带 legacy 上线须显式 ALLOW_LEGACY_AGENT_TOKEN=1。
  env_legacy="$(grep -E '^[[:space:]]*(export[[:space:]]+)?PORTAL_AGENT_TOKEN=' .env 2>/dev/null \
    | tail -n 1 | cut -d= -f2- | tr -d '\r"[:space:]' || true)"
  if [ -n "${PORTAL_AGENT_TOKEN:-}" ] || [ -n "$env_legacy" ]; then
    if [ "${ALLOW_LEGACY_AGENT_TOKEN:-}" != "1" ]; then
      pre_fail "PORTAL_AGENT_TOKEN 非空（shell=$([ -n "${PORTAL_AGENT_TOKEN:-}" ] && echo '<已设置>' || echo '<未设>')，.env=$([ -n "$env_legacy" ] && echo '<已设置>' || echo '<空>')）。legacy 共享 token 无 agent 身份绑定，双因子认证退化为单因子。"
      echo "   修复：迁移为逐机 token（管理页「机器凭据」签发），agent 传值走" >&2
      echo "   portal-agent-compose.yml 的 0600 落盘文件 agent-token（勿填本变量——" >&2
      echo "   它是 backend 的 legacy 回退开关），.env 置 PORTAL_AGENT_TOKEN= 后重跑；" >&2
      echo "   确要临时保留 legacy，以 ALLOW_LEGACY_AGENT_TOKEN=1 重跑本脚本显式确认。" >&2
    fi
  fi

  if [ -n "$precheck_failed" ]; then
    echo "!! prod 预检未通过，中止部署（逐项修复后重跑）。" >&2
    exit 1
  fi
fi

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
