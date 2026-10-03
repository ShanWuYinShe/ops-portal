package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// agent 上报状态（内存保存；Phase 2 命令队列落盘，状态仍内存）。

type agentSite struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Head   string `json:"head"`
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
}

type agentContainer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Service string `json:"service"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Image   string `json:"image"`
}

type agentHeartbeat struct {
	AgentID         string           `json:"portal_agent_id"`
	Hostname        string           `json:"hostname"`
	ComposeProjects []string         `json:"compose_projects"`
	Sites           []agentSite      `json:"sites"`
	Containers      []agentContainer `json:"containers"`
}

type agentState struct {
	Heartbeat agentHeartbeat `json:"heartbeat"`
	LastSeen  time.Time      `json:"last_seen"`
}

var agentStore = struct {
	sync.Mutex
	m map[string]agentState
}{m: make(map[string]agentState)}

func apiAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var hb agentHeartbeat
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&hb); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if hb.AgentID == "" {
		writeError(w, http.StatusBadRequest, "portal_agent_id required")
		return
	}
	// token 绑定的 portal_agent_id 与上报身份必须一致（防持合法 token 冒充其他 agent
	// 上报状态、偷走下发给他人的命令；legacy 单 token 回退时不绑定）。
	if bound := agentIDFromContext(r); bound != "" && bound != hb.AgentID {
		log.Printf("portal: heartbeat portal_agent_id mismatch token=%s body=%s", bound, hb.AgentID)
		writeError(w, http.StatusForbidden, "portal_agent_id mismatch with token")
		return
	}
	agentStore.Lock()
	agentStore.m[hb.AgentID] = agentState{Heartbeat: hb, LastSeen: time.Now()}
	agentStore.Unlock()
	// 取走该 agent 的排队命令（agent 执行后经 /report 回传结果）
	writeOK(w, map[string]any{"commands": takeCommands(hb.AgentID)})
}

type commandReport struct {
	ID     string `json:"id"`
	Status string `json:"status"` // done / failed
	Result string `json:"result"`
}

func apiAgentReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		AgentID string          `json:"portal_agent_id"`
		Reports []commandReport `json:"reports"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if bound := agentIDFromContext(r); bound != "" && bound != body.AgentID {
		log.Printf("portal: report portal_agent_id mismatch token=%s body=%s", bound, body.AgentID)
		writeError(w, http.StatusForbidden, "portal_agent_id mismatch with token")
		return
	}
	for _, rep := range body.Reports {
		if err := reportCommand(body.AgentID, rep.ID, rep.Status, rep.Result); err != nil {
			log.Printf("portal: report %s failed: %v", rep.ID, err)
		}
	}
	writeOK(w, nil)
}

func apiPortalAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// 锁内只做快照拷贝，pending 统计的 DB 查询放到锁外，避免拖住心跳写入。
	agentStore.Lock()
	snap := make(map[string]agentState, len(agentStore.m))
	for id, st := range agentStore.m {
		snap[id] = st
	}
	agentStore.Unlock()
	out := make([]map[string]any, 0, len(snap))
	for id, st := range snap {
		pending := 0
		for _, c := range listCommands(id) {
			if c.Status == "queued" || c.Status == "acked" {
				pending++
			}
		}
		out = append(out, map[string]any{
			"portal_agent_id": id,
			"last_seen":       st.LastSeen.Format(time.RFC3339),
			// 在线态以后端判定为准（AGENT_OFFLINE_AFTER），前端不再自算
			"online":    agentOnlineAt(st.LastSeen),
			"pending":   pending,
			"heartbeat": st.Heartbeat,
		})
	}
	writeOK(w, map[string]any{"agents": out})
}

func apiPortalWhoami(w http.ResponseWriter, r *http.Request) {
	id := certFromRequest(r)
	u := userFromContext(r)
	writeOK(w, map[string]any{
		// 边缘透传的 X-Cert-Verify 原值是字符串（"true"/"false"/空），
		// 对外统一为布尔：非假值即边缘已验证（见 auth.go edgeVerified）。
		"verified":    edgeVerified(id),
		"serial":      id.Serial,
		"subject":     id.Subject,
		"fingerprint": id.Fingerprint,
		"trusted":     id.Trusted,
		"username":    u.Username,
		"role":        u.Role,
		"dev_no_cert": getEnv("DEV_ALLOW_NO_CERT", "") != "",
	})
}

func apiPortalHealth(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, map[string]any{"status": "ok", "service": "ops-portal"})
}

// requireAgentTokenV2 机器树双因子：
//   - 经 nginx（即经 CF 边缘 mTLS）的请求：机器证书须在表且 kind=portal-agent，
//     且证书 label（=portal_agent_id）与 bearer token 的 portal_agent_id 一致；
//   - 同宿主机内网直连（不产生证书头，网络仅 portal_net 可达）：仅 token。
func requireAgentTokenV2(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if got == "" {
			writeError(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		agentID, ok := validAgentToken(got)
		if !ok {
			if want := strings.TrimSpace(getEnv("PORTAL_AGENT_TOKEN", "")); want != "" && constantTimeEqual(got, want) {
				// legacy 共享 token 无 agent 身份绑定（bound 置空 = 心跳身份不比对），
				// 任何持 token 者可冒充任意 agent：每次使用都高声留痕，推动迁移。
				log.Printf("portal: WARNING: legacy shared PORTAL_AGENT_TOKEN used from %s (no agent identity binding); migrate to per-agent tokens", clientIP(r))
				agentID, ok = "legacy", true
			}
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		id := certFromRequest(r)
		if id.Trusted {
			c := findActiveCert(id.Serial, id.Fingerprint)
			if c == nil || c.Kind != "portal-agent" || c.Label != agentID {
				log.Printf("portal: agent cert/token mismatch serial=%s", maskMiddle(id.Serial))
				writeError(w, http.StatusForbidden, "agent certificate mismatch")
				return
			}
		}
		// token 绑定的 portal_agent_id 塞进 context，心跳/回传强制比对上报身份；
		// legacy 单 token 回退（过渡期 dev）传空串表示不绑定。
		bound := agentID
		if bound == "legacy" {
			bound = ""
		}
		next(w, r.WithContext(contextWithAgentID(r, bound)))
	}
}

func main() {
	port := getEnv("PORTAL_BACKEND_PORT", "8319")

	// 数据库先行：打不开直接 fail fast；旧 JSON 有数据则一次性导入。
	if _, err := getDB(); err != nil {
		log.Fatalf("portal: open db failed: %v", err)
	}
	purgeOldAudit()
	purgeOldWebhooks()
	// dev 免证开关一旦误入生产等于管理面全开（无证按 ops.admin 放行），
	// 启动即高声提示，让日志里看得见。
	if getEnv("DEV_ALLOW_NO_CERT", "") != "" {
		log.Printf("portal: WARNING: DEV_ALLOW_NO_CERT 已启用——无证请求按 ops.admin 放行，仅限本地联调，生产必须留空")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/portal/health", apiPortalHealth)
	mux.HandleFunc("GET /api/portal/whoami", requireRole("ops.viewer", apiPortalWhoami))
	mux.HandleFunc("GET /api/portal/agents", requireRole("ops.viewer", apiPortalAgents))

	// 证书库（读 viewer+，写 admin）
	mux.HandleFunc("GET /api/portal/certs", requireRole("ops.viewer", apiCertsList))
	mux.HandleFunc("POST /api/portal/certs/register", requireRole("ops.admin", apiCertsAdd))
	mux.HandleFunc("POST /api/portal/certs/revoke", requireRole("ops.admin", apiCertsRevoke))
	mux.HandleFunc("POST /api/portal/certs/rename", requireRole("ops.admin", apiCertsRename))

	// agent token（admin）+ 一次性注册令牌（admin 签发，被管机自助兑换）
	mux.HandleFunc("GET /api/portal/agent-tokens", requireRole("ops.admin", apiAgentTokens))
	mux.HandleFunc("POST /api/portal/agent-tokens/issue", requireRole("ops.admin", apiAgentTokensIssue))
	mux.HandleFunc("POST /api/portal/agent-tokens/revoke", requireRole("ops.admin", apiAgentTokensRevoke))
	mux.HandleFunc("POST /api/portal/enroll-tokens/issue", requireRole("ops.admin", apiEnrollTokensIssue))

	// 命令（读 viewer+，入队 operator+）
	mux.HandleFunc("GET /api/portal/commands", requireRole("ops.viewer", apiCommandsList))
	mux.HandleFunc("POST /api/portal/commands/enqueue", requireRole("ops.operator", apiCommandsEnqueue))

	// 审计（admin）+ 在线备份（admin）
	mux.HandleFunc("GET /api/portal/audit", requireRole("ops.admin", apiAuditList))
	mux.HandleFunc("POST /api/portal/backup", requireRole("ops.admin", apiBackup))

	// 用户与证书绑定（admin）
	mux.HandleFunc("GET /api/portal/users", requireRole("ops.admin", apiUsersList))
	mux.HandleFunc("POST /api/portal/users/upsert", requireRole("ops.admin", apiUsersUpsert))
	mux.HandleFunc("POST /api/portal/users/remove", requireRole("ops.admin", apiUsersRemove))

	// 统一网站视图（心跳 + 原生注册合并，viewer+；签发类操作仍 admin）
	mux.HandleFunc("POST /api/portal/service-cert/issue", requireRole("ops.admin", apiServiceCertIssue))
	mux.HandleFunc("POST /api/portal/site-certificates/issue", requireRole("ops.admin", apiSiteCertificateIssue))
	mux.HandleFunc("GET /api/portal/sites", requireRole("ops.viewer", apiPortalSites))
	mux.HandleFunc("POST /api/portal/sites/unregister", requireRole("ops.admin", apiSitesUnregister))
	// 被管网站自助注册（须出示 kind=site 证书，边缘 mTLS 照常强制）
	mux.HandleFunc("POST /api/site/register", requireCert(apiSiteRegister))

	// 机器证书包签发（admin）：机器证书 + 机器 token 一次发放
	mux.HandleFunc("POST /api/portal/agent-certificates/issue", requireRole("ops.admin", apiAgentCertificateIssue))

	// agent 树（双因子：机器证书 + token；同宿主机内网直连仅 token，见 exec 说明）
	// enroll 兑换不套 token 中间件：enroll 令牌本身即凭证（一次性，30 分钟）。
	mux.HandleFunc("POST /api/portal-agent/enroll", apiAgentEnroll)
	mux.HandleFunc("POST /api/portal-agent/heartbeat", requireAgentTokenV2(apiAgentHeartbeat))
	mux.HandleFunc("POST /api/portal-agent/report", requireAgentTokenV2(apiAgentReport))

	// webhook：入站接收（第三方回调，无证书鉴权，靠 per-endpoint secret 的 HMAC 校验）
	// 与管理面（沿用 requireRole 门禁）。注意 /api/hooks/{slug} 不套 requireCert——
	// 它由公网第三方调用，没有 mTLS 客户端证书。
	mux.HandleFunc("POST /api/hooks/{slug}", apiWebhookReceive)
	mux.HandleFunc("GET /api/portal/webhooks/endpoints", requireRole("ops.viewer", apiWebhookEndpointsList))
	mux.HandleFunc("POST /api/portal/webhooks/endpoints/upsert", requireRole("ops.admin", apiWebhookEndpointsUpsert))
	mux.HandleFunc("POST /api/portal/webhooks/endpoints/remove", requireRole("ops.admin", apiWebhookEndpointsRemove))
	mux.HandleFunc("POST /api/portal/webhooks/endpoints/rotate-secret", requireRole("ops.admin", apiWebhookEndpointsRotate))
	mux.HandleFunc("GET /api/portal/webhooks/sources", requireRole("ops.viewer", apiWebhookSourcesList))
	mux.HandleFunc("POST /api/portal/webhooks/sources/upsert", requireRole("ops.admin", apiWebhookSourcesUpsert))
	mux.HandleFunc("POST /api/portal/webhooks/sources/remove", requireRole("ops.admin", apiWebhookSourcesRemove))
	mux.HandleFunc("GET /api/portal/webhooks/deliveries", requireRole("ops.viewer", apiWebhookDeliveriesList))
	mux.HandleFunc("GET /api/portal/webhooks/deliveries/get", requireRole("ops.viewer", apiWebhookDeliveriesGet))
	mux.HandleFunc("POST /api/portal/webhooks/deliveries/replay", requireRole("ops.operator", apiWebhookDeliveriesReplay))
	mux.HandleFunc("GET /api/portal/webhooks/stats", requireRole("ops.viewer", apiWebhookStats))

	// 前端 SPA（SvelteKit adapter-static 产物，Docker 构建时拷到 /app/ui）。
	// 文件存在则直出，未知路径回退 index.html（客户端路由 /dashboard 等）。
	// 注意：未知 /api/ 路径不能回退 index.html（会 200 返回 HTML，前端把
	// 错误吞掉），一律 404 JSON；非 GET 的未知 /api/ 由 mux 直接 405，
	// 与历史行为一致（不能注册 "/api/" 兜底 pattern——与 "GET /" 冲突 panic）。
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		// CSP 只对 HTML 文档有意义（策略见 csp.go；nginx 不重复设置）。
		w.Header().Set("Content-Security-Policy", cspHeader())
		upath := filepath.Clean(uiDir + r.URL.Path)
		if upath != uiDir && !strings.HasPrefix(upath, uiDir+"/") {
			http.NotFound(w, r)
			return
		}
		if st, err := os.Stat(upath); err == nil && !st.IsDir() {
			http.ServeFile(w, r, upath)
			return
		}
		http.ServeFile(w, r, uiDir+"/index.html")
	})

	srv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// 慢请求上限：最重的在线备份实测 ~0.1s/10 万行审计，60s 余量充足；
		// IdleTimeout 复用连接保活上限，防空闲连接占住 fd。
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	// 原生注册站点的 ops 接口连通拨测（后台循环，结果进统一网站视图）。
	startProbeLoop()
	// agent 心跳超时看门狗：online↔offline 跃迁进告警（alerts.go）。
	startAgentWatchdog()
	// 审计保留期按天兜底清理（启动时已清一次；不常备份的部署也保生效）。
	startRetentionLoop()
	// webhook 出站转发 worker：扫描 pending 投递，按退避策略重试。
	startWebhookDispatcher()
	log.Printf("ops-portal backend listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
