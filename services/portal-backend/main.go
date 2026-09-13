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
	AgentID         string           `json:"agent_id"`
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
		writeError(w, http.StatusBadRequest, "agent_id required")
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
		AgentID string          `json:"agent_id"`
		Reports []commandReport `json:"reports"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
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
	agentStore.Lock()
	out := make([]map[string]any, 0, len(agentStore.m))
	for id, st := range agentStore.m {
		pending := 0
		for _, c := range listCommands(id) {
			if c.Status == "queued" || c.Status == "acked" {
				pending++
			}
		}
		out = append(out, map[string]any{
			"agent_id":  id,
			"last_seen": st.LastSeen.Format(time.RFC3339),
			"pending":   pending,
			"heartbeat": st.Heartbeat,
		})
	}
	agentStore.Unlock()
	writeOK(w, map[string]any{"agents": out})
}

func apiPortalWhoami(w http.ResponseWriter, r *http.Request) {
	id := certFromRequest(r)
	u := userFromContext(r)
	writeOK(w, map[string]any{
		"verified":    id.Verified,
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

// requireAgentTokenV2 先查 token 库（多 token），回退兼容旧单 AGENT_TOKEN。
func requireAgentTokenV2(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if got == "" {
			writeError(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		if _, ok := validAgentToken(got); ok {
			next(w, r)
			return
		}
		// 回退：旧单 token（本地 dev 过渡）
		if want := strings.TrimSpace(getEnv("AGENT_TOKEN", "")); want != "" && constantTimeEqual(got, want) {
			next(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid agent token")
	}
}

func main() {
	port := getEnv("PORTAL_BACKEND_PORT", "8319")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/portal/health", apiPortalHealth)
	mux.HandleFunc("GET /api/portal/whoami", requireRole("ops.viewer", apiPortalWhoami))
	mux.HandleFunc("GET /api/portal/agents", requireRole("ops.viewer", apiPortalAgents))

	// 证书库（读 viewer+，写 admin）
	mux.HandleFunc("GET /api/portal/certs", requireRole("ops.viewer", apiCertsList))
	mux.HandleFunc("POST /api/portal/certs/register", requireRole("ops.admin", apiCertsAdd))
	mux.HandleFunc("POST /api/portal/certs/revoke", requireRole("ops.admin", apiCertsRevoke))

	// agent token（admin）
	mux.HandleFunc("GET /api/portal/agent-tokens", requireRole("ops.admin", apiAgentTokens))
	mux.HandleFunc("POST /api/portal/agent-tokens/issue", requireRole("ops.admin", apiAgentTokensIssue))
	mux.HandleFunc("POST /api/portal/agent-tokens/revoke", requireRole("ops.admin", apiAgentTokensRevoke))

	// 命令（读 viewer+，入队 operator+）
	mux.HandleFunc("GET /api/portal/commands", requireRole("ops.viewer", apiCommandsList))
	mux.HandleFunc("POST /api/portal/commands/enqueue", requireRole("ops.operator", apiCommandsEnqueue))

	// 审计（admin）
	mux.HandleFunc("GET /api/portal/audit", requireRole("ops.admin", apiAuditList))

	// 注册令牌（admin 签发）与 agent 自助注册
	mux.HandleFunc("POST /api/portal/agent-tokens/enroll-token", requireRole("ops.admin", apiAgentEnrollTokenIssue))
	mux.HandleFunc("POST /api/agent/enroll", apiAgentEnroll)

	// agent 树（token 鉴权）
	mux.HandleFunc("POST /api/agent/heartbeat", requireAgentTokenV2(apiAgentHeartbeat))
	mux.HandleFunc("POST /api/agent/report", requireAgentTokenV2(apiAgentReport))

	// 前端 SPA（SvelteKit adapter-static 产物，Docker 构建时拷到 /app/ui）。
	// 文件存在则直出，未知路径回退 index.html（客户端路由 /dashboard 等）。
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		upath := filepath.Clean("/app/ui" + r.URL.Path)
		if upath != "/app/ui" && !strings.HasPrefix(upath, "/app/ui/") {
			http.NotFound(w, r)
			return
		}
		if st, err := os.Stat(upath); err == nil && !st.IsDir() {
			http.ServeFile(w, r, upath)
			return
		}
		http.ServeFile(w, r, "/app/ui/index.html")
	})

	srv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("ops-portal backend listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
