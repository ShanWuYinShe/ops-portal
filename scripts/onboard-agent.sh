#!/usr/bin/env bash
# ops-portal 被管机一键接入（自助注册，免手工复制长效 token）
#
# 前置：本机已 checkout ops-portal 仓库（脚本用 services/agent 构建镜像）；
#       管理员在 portal 侧交付 bundle.json（含 enroll_token/client_cert/
#       client_key：注册令牌在「Agent 令牌」页签发，机器证书在
#       「机器证书包」接口签发后拼成 bundle 交付本机）。
#
# 用法：
#   bash scripts/onboard-agent.sh \
#     --portal https://ops.yinghuoren.com \
#     --agent-id agent-xxx-01 \
#     --bundle bundle.json \
#     --project hotspot --site hotspot:/srv/hotspot --deploy hotspot:/srv/hotspot \
#     [--local]            # portal 与本 agent 同一 docker 宿主机时使用（走内网）
#
# 行为：落盘机器证书 → 生成 agent-compose.generated.yml（含 AGENT_ENROLL_TOKEN
#       与证书/token 落盘挂载）→ 构建启动 → agent 首次启动自助兑换正式 token
#       并落盘到 agent-secrets/ → 到 portal「站点」页确认心跳出现。
#       长效 token 永不经人工手；enroll 令牌 30 分钟有效、单次使用。

set -euo pipefail

PORTAL="https://ops.yinghuoren.com"
AGENT_ID="" BUNDLE="" LOCAL=0
PROJECTS=() SITES=() DEPLOYS=()
COMPOSE_OUT="agent-compose.generated.yml"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --portal) PORTAL="$2"; shift 2 ;;
    --agent-id) AGENT_ID="$2"; shift 2 ;;
    --bundle) BUNDLE="$2"; shift 2 ;;
    --project) PROJECTS+=("$2"); shift 2 ;;
    --site) SITES+=("$2"); shift 2 ;;
    --deploy) DEPLOYS+=("$2"); shift 2 ;;
    --local) LOCAL=1; shift ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

[[ -n "$AGENT_ID" && -n "$BUNDLE" ]] || { echo "必须提供 --agent-id 与 --bundle" >&2; exit 1; }
[[ -f "$BUNDLE" ]] || { echo "bundle 不存在: $BUNDLE" >&2; exit 1; }
[[ -d services/agent ]] || { echo "请在 ops-portal 仓库根目录执行（需要 services/agent 构建上下文）" >&2; exit 1; }

join() { local IFS=,; echo "$*"; }

# 1) 读取接入包：机器证书落盘（600），enroll 令牌只进 compose env
BUNDLE_PATH="$BUNDLE" python3 - <<'EOF'
import json, os
d = json.load(open(os.environ["BUNDLE_PATH"]))
for k in ("enroll_token", "client_cert", "client_key"):
    if not d.get(k):
        raise SystemExit(f"bundle 缺 {k}")
open("agent-client.crt", "w").write(d["client_cert"])
open("agent-client.key", "w").write(d["client_key"])
open(".enroll_token.tmp", "w").write(d["enroll_token"])
EOF
chmod 600 agent-client.crt agent-client.key .enroll_token.tmp
ENROLL=$(cat .enroll_token.tmp)
rm -f .enroll_token.tmp
mkdir -p agent-secrets
chmod 700 agent-secrets
echo "==> 机器证书已落盘（agent-client.crt/key），enroll 令牌已就位（勿外传）"

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
      # 首次启动自助兑换正式 token（单次有效），成功后落盘到 AGENT_TOKEN_FILE；
      # 重启优先读落盘 token，不再消耗 enroll。
      - AGENT_ENROLL_TOKEN=${ENROLL}
      - AGENT_TOKEN_FILE=/agent/secrets/agent-token
      # 机器客户端证书（边缘 mTLS 出示；同机内网直连不受影响）
      - AGENT_CERT=/agent/certs/client.crt
      - AGENT_KEY=/agent/certs/client.key
      - PORTAL_URL=${PORTAL_URL}
      - AGENT_COMPOSE_PROJECTS=${PROJ_CSV}
      - AGENT_SITES=${SITE_CSV}
      - AGENT_DEPLOY_DIRS=${DEPLOY_CSV}
      - HEARTBEAT_INTERVAL=10s
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./agent-client.crt:/agent/certs/client.crt:ro
      - ./agent-client.key:/agent/certs/client.key:ro
      - ./agent-secrets:/agent/secrets
${MOUNTS}${NETWORK}
${NETWORKS_DEF}
EOF

# 3) 构建启动
docker compose -f "$COMPOSE_OUT" up -d --build
echo "==> agent 已启动（容器 ${AGENT_ID}），首次启动自动兑换 token 并上报心跳"
echo "==> 到 portal「站点与服务」页确认心跳出现（≤30 秒，含兑换）。"
[[ $LOCAL -eq 1 ]] && echo "提示：--local 模式要求本机已运行 ops-portal 主 compose（网络 ops-portal_portal_net）。"
echo "==> 更换 token/吊销：portal「Agent 令牌」页吊销后，删 agent-secrets/agent-token，"
echo "    重新签发 enroll 令牌并更新 $COMPOSE_OUT 中 AGENT_ENROLL_TOKEN，再 up -d。"
