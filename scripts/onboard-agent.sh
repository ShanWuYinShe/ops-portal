#!/usr/bin/env bash
# ops-portal 被管机一键接入（自助注册换证，免手工复制 token）
#
# 前置：本机已 checkout ops-portal 仓库（脚本用 services/agent 构建镜像）；
#       portal 已签发一次性注册令牌（enroll_ 前缀，30 分钟有效，单次使用）。
#
# 用法：
#   bash scripts/onboard-agent.sh \
#     --portal https://ops.yinghuoren.com \
#     --agent-id agent-xxx-01 \
#     --enroll-token enroll_xxxxxxxx \
#     --project hotspot --site hotspot:/srv/hotspot --deploy hotspot:/srv/hotspot \
#     [--local]            # portal 与本 agent 同一 docker 宿主机时使用（走内网）
#
# 行为：curl 注册换正式 token → 生成 agent-compose.generated.yml → 构建启动 →
#       提示到 portal「站点」页确认心跳出现。token 只写入该 compose 的 env。

set -euo pipefail

PORTAL="https://ops.yinghuoren.com"
AGENT_ID="" ENROLL="" LOCAL=0
PROJECTS=() SITES=() DEPLOYS=()
COMPOSE_OUT="agent-compose.generated.yml"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --portal) PORTAL="$2"; shift 2 ;;
    --agent-id) AGENT_ID="$2"; shift 2 ;;
    --enroll-token) ENROLL="$2"; shift 2 ;;
    --project) PROJECTS+=("$2"); shift 2 ;;
    --site) SITES+=("$2"); shift 2 ;;
    --deploy) DEPLOYS+=("$2"); shift 2 ;;
    --local) LOCAL=1; shift ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

[[ -n "$AGENT_ID" && -n "$ENROLL" ]] || { echo "必须提供 --agent-id 与 --enroll-token" >&2; exit 1; }
[[ -d services/agent ]] || { echo "请在 ops-portal 仓库根目录执行（需要 services/agent 构建上下文）" >&2; exit 1; }

join() { local IFS=,; echo "$*"; }

# 1) 自助注册换取正式 token
echo "==> 向 $PORTAL 注册 $AGENT_ID ..."
RESP=$(curl -fsS --max-time 20 -X POST "$PORTAL/api/agent/enroll" \
  -H 'Content-Type: application/json' \
  -d "{\"enroll_token\":\"$ENROLL\",\"agent_id\":\"$AGENT_ID\"}")
TOKEN=$(printf '%s' "$RESP" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
[[ -n "$TOKEN" ]] || { echo "注册失败: $RESP" >&2; exit 1; }
echo "==> 已换取正式 token（已写入生成的 compose，勿外传）"

# 2) 生成 compose
if [[ $LOCAL -eq 1 ]]; then
  PORTAL_URL="http://portal-backend:8319"
  NETWORK=$'    networks:\n      - ops-portal_portal_net'
  NETWORKS_DEF=$'\nnetworks:\n  ops-portal_portal_net:\n    external: true'
else
  PORTAL_URL="$PORTAL"
  NETWORK="" NETWORKS_DEF=""
fi

PROJ_CSV=$(join "${PROJECTS[@]:-}")
SITE_CSV=$(join "${SITES[@]:-}")
DEPLOY_CSV=$(join "${DEPLOYS[@]:-}")
MOUNTS=""
for d in "${DEPLOYS[@]:-}"; do
  [[ -z "$d" ]] && continue
  path="${d#*:}"
  MOUNTS+="      - ${path}:${path}:ro"$'\n'
done

cat > "$COMPOSE_OUT" <<EOF
# 由 scripts/onboard-agent.sh 生成（$(date -Iseconds)）
services:
  ops-agent:
    build:
      context: ./services/agent
      dockerfile: Dockerfile
    container_name: ${AGENT_ID}
    restart: unless-stopped
    init: true
    user: "0:0"
    group_add: ["\${DOCKER_GID:-988}"]
    security_opt: ["no-new-privileges:true"]
    cap_drop: [ALL]
    environment:
      - TZ=Asia/Shanghai
      - AGENT_ID=${AGENT_ID}
      - AGENT_TOKEN=${TOKEN}
      - PORTAL_URL=${PORTAL_URL}
      - AGENT_COMPOSE_PROJECTS=${PROJ_CSV}
      - AGENT_SITES=${SITE_CSV}
      - AGENT_DEPLOY_DIRS=${DEPLOY_CSV}
      - HEARTBEAT_INTERVAL=10s
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
${MOUNTS}${NETWORK}
${NETWORKS_DEF}
EOF

# 3) 构建启动
docker compose -f "$COMPOSE_OUT" up -d --build
echo "==> agent 已启动（容器 ${AGENT_ID}）"
echo "==> 到 portal「站点与服务」页确认心跳出现（≤10 秒）。"
[[ $LOCAL -eq 1 ]] && echo "提示：--local 模式要求本机已运行 ops-portal 主 compose（网络 ops-portal_portal_net）。"
echo "==> 更换 token/吊销：portal「Agent 令牌」页操作后，编辑 $COMPOSE_OUT 中 AGENT_TOKEN 并重新 up -d。"
