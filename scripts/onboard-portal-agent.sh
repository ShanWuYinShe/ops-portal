#!/usr/bin/env bash
# ops-portal 被管机一键接入（自助注册，免手工复制长效 token）
#
# 前置：本机已 checkout ops-portal 仓库（脚本用 services/portal-agent 构建镜像）；
#       管理员在 portal 侧交付 bundle.json（含 enroll_token/client_cert/
#       client_key：注册令牌在「机器凭据」页签发，机器证书在
#       「机器凭据」页的机器证书包签发后拼成 bundle 交付本机）。
#
# 用法：
#   bash scripts/onboard-portal-agent.sh \
#     --portal https://ops.yinghuoren.com \
#     --agent-id agent-xxx-01 \
#     --bundle bundle.json \
#     --project hotspot --site hotspot:/srv/hotspot --deploy hotspot:/srv/hotspot \
#     [--local]            # portal 与本 agent 同一 docker 宿主机时使用（走内网）
#
# 行为：落盘机器证书 → 生成 portal-agent-compose.generated.yml（含 PORTAL_AGENT_ENROLL_TOKEN
#       与证书/token 落盘挂载）→ 构建启动 → agent 首次启动自助兑换正式 token
#       并落盘到 portal-agent-secrets/ → 到 portal「站点与服务」页确认心跳出现。
#       长效 token 永不经人工手；enroll 令牌 30 分钟有效、单次使用。

set -euo pipefail

PORTAL="https://ops.yinghuoren.com"
PORTAL_AGENT_ID="" BUNDLE="" LOCAL=0
PROJECTS=() SITES=() DEPLOYS=()
COMPOSE_OUT="portal-agent-compose.generated.yml"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --portal) PORTAL="$2"; shift 2 ;;
    --agent-id) PORTAL_AGENT_ID="$2"; shift 2 ;;
    --bundle) BUNDLE="$2"; shift 2 ;;
    --project) PROJECTS+=("$2"); shift 2 ;;
    --site) SITES+=("$2"); shift 2 ;;
    --deploy) DEPLOYS+=("$2"); shift 2 ;;
    --local) LOCAL=1; shift ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

[[ -n "$PORTAL_AGENT_ID" && -n "$BUNDLE" ]] || { echo "必须提供 --agent-id 与 --bundle" >&2; exit 1; }
[[ -f "$BUNDLE" ]] || { echo "bundle 不存在: $BUNDLE" >&2; exit 1; }
[[ -d services/portal-agent ]] || { echo "请在 ops-portal 仓库根目录执行（需要 services/portal-agent 构建上下文）" >&2; exit 1; }

join() { local IFS=,; echo "$*"; }

# 1) 读取接入包：机器证书落盘（600），enroll 令牌只进 compose env
BUNDLE_PATH="$BUNDLE" python3 - <<'EOF'
import json, os
d = json.load(open(os.environ["BUNDLE_PATH"]))
for k in ("enroll_token", "client_cert", "client_key"):
    if not d.get(k):
        raise SystemExit(f"bundle 缺 {k}")
open("portal-agent-client.crt", "w").write(d["client_cert"])
open("portal-agent-client.key", "w").write(d["client_key"])
open(".enroll_token.tmp", "w").write(d["enroll_token"])
EOF
chmod 600 portal-agent-client.crt portal-agent-client.key .enroll_token.tmp
ENROLL=$(cat .enroll_token.tmp)
rm -f .enroll_token.tmp
mkdir -p portal-agent-secrets
chmod 700 portal-agent-secrets
echo "==> 机器证书已落盘（portal-agent-client.crt/key），enroll 令牌已就位（勿外传）"

# agent 容器以非 root uid 1001（镜像内建 appuser）运行：需读客户端证书
# （600）、enroll 兑换后写 token 到 portal-agent-secrets/。落盘产物须归
# uid 1001 所有，否则容器内 cap_drop ALL 读/写都会 permission denied。
#   - 以 root 执行本脚本：自动 chown；
#   - 本身就是 uid 1001：无需处理；
#   - 其他情况：给出修复命令后中止（带病启动会表现为心跳全挂）。
fix_owner_1001() {
  local uid gid
  uid="$(id -u)" gid="$(id -g)"
  if [ "$uid" -eq 0 ]; then
    chown -R 1001:1001 "$@"
  elif [ "$uid" -eq 1001 ] && [ "$gid" -eq 1001 ]; then
    :
  else
    echo "!! 容器内 agent 以 uid 1001 运行，但当前用户（uid=$uid）无法移交文件属主。" >&2
    echo "   请手动执行后重试：sudo chown -R 1001:1001 portal-agent-client.crt portal-agent-client.key portal-agent-secrets" >&2
    exit 1
  fi
}
fix_owner_1001 portal-agent-client.crt portal-agent-client.key portal-agent-secrets

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
# 由 scripts/onboard-portal-agent.sh 生成（$(date -Iseconds)）
services:
  portal-agent:
    build:
      context: ./services/portal-agent
      dockerfile: Dockerfile
    container_name: ${PORTAL_AGENT_ID}
    restart: unless-stopped
    init: true
    # 非 root：镜像内建 appuser(uid/gid=1001)；docker.sock 凭 group_add 的
    # 宿主 docker gid 访问。证书与 token 落盘目录已由本脚本 chown 1001。
    user: "1001:1001"
    group_add: ["\${DOCKER_GID:-988}"]
    security_opt: ["no-new-privileges:true"]
    cap_drop: [ALL]
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1:18080/health"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 15s
    environment:
      - TZ=Asia/Shanghai
      - PORTAL_AGENT_ID=${PORTAL_AGENT_ID}
      # 首次启动自助兑换正式 token（单次有效），成功后落盘到 PORTAL_AGENT_TOKEN_FILE；
      # 重启优先读落盘 token，不再消耗 enroll。
      - PORTAL_AGENT_ENROLL_TOKEN=${ENROLL}
      - PORTAL_AGENT_TOKEN_FILE=/agent/secrets/agent-token
      # 机器客户端证书（边缘 mTLS 出示；同机内网直连不受影响）
      - PORTAL_AGENT_CERT=/agent/certs/client.crt
      - PORTAL_AGENT_KEY=/agent/certs/client.key
      - PORTAL_URL=${PORTAL_URL}
      - PORTAL_AGENT_COMPOSE_PROJECTS=${PROJ_CSV}
      - PORTAL_AGENT_SITES=${SITE_CSV}
      - PORTAL_AGENT_DEPLOY_DIRS=${DEPLOY_CSV}
      - HEARTBEAT_INTERVAL=10s
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./portal-agent-client.crt:/agent/certs/client.crt:ro
      - ./portal-agent-client.key:/agent/certs/client.key:ro
      - ./portal-agent-secrets:/agent/secrets
${MOUNTS}${NETWORK}
${NETWORKS_DEF}
EOF

# 3) 构建启动
docker compose -f "$COMPOSE_OUT" up -d --build
echo "==> agent 已启动（容器 ${PORTAL_AGENT_ID}），首次启动自动兑换 token 并上报心跳"
echo "==> 到 portal「站点与服务」页确认心跳出现（≤30 秒，含兑换）。"
[[ $LOCAL -eq 1 ]] && echo "提示：--local 模式要求本机已运行 ops-portal 主 compose（网络 ops-portal_portal_net）。"
echo "==> 更换 token/吊销：portal「机器凭据」页吊销后，删 portal-agent-secrets/agent-token，"
echo "    重新签发 enroll 令牌并更新 $COMPOSE_OUT 中 PORTAL_AGENT_ENROLL_TOKEN，再 up -d。"
