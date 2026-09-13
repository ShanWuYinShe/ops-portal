package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// agent 上报状态（内存保存，Phase 1 不持久化；Phase 2 落盘 SQLite）。

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
	// Phase 1：命令队列为空，返回空数组占位（Phase 2 下发启停重启）
	writeOK(w, map[string]any{"commands": []any{}})
}

func apiPortalAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	agentStore.Lock()
	out := make([]map[string]any, 0, len(agentStore.m))
	for id, st := range agentStore.m {
		out = append(out, map[string]any{
			"agent_id":  id,
			"last_seen": st.LastSeen.Format(time.RFC3339),
			"heartbeat": st.Heartbeat,
		})
	}
	agentStore.Unlock()
	writeOK(w, map[string]any{"agents": out})
}

func apiPortalWhoami(w http.ResponseWriter, r *http.Request) {
	id := certFromRequest(r)
	writeOK(w, map[string]any{
		"verified":    id.Verified,
		"serial":      id.Serial,
		"subject":     id.Subject,
		"fingerprint": id.Fingerprint,
		"trusted":     id.Trusted,
		"dev_no_cert": getEnv("DEV_ALLOW_NO_CERT", "") != "",
	})
}

func apiPortalHealth(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, map[string]any{"status": "ok", "service": "ops-portal"})
}

func main() {
	port := getEnv("PORTAL_BACKEND_PORT", "8319")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/portal/health", apiPortalHealth)
	mux.HandleFunc("GET /api/portal/whoami", requireCert(apiPortalWhoami))
	mux.HandleFunc("GET /api/portal/agents", requireCert(apiPortalAgents))
	mux.HandleFunc("POST /api/agent/heartbeat", requireAgentToken(apiAgentHeartbeat))

	// 前端静态页（Docker 构建时把 ui/dist 拷到 /app/ui）
	mux.Handle("GET /", http.FileServer(http.Dir("/app/ui")))

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
