package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 审计取真实客户端 IP：受信代理透传的 X-Real-IP 优先（nginx 已按
// CF-Connecting-IP 解析），非受信来源一律退回连接对端，不采信头。
func TestClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:443" // 默认受信网段
	req.Header.Set("X-Real-IP", "203.0.113.7")
	if got := clientIP(req); got != "203.0.113.7" {
		t.Fatalf("trusted proxy: clientIP = %q, want X-Real-IP", got)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "192.0.2.9:1234" // 非受信
	req2.Header.Set("X-Real-IP", "203.0.113.7")
	if got := clientIP(req2); got != "192.0.2.9:1234" {
		t.Fatalf("untrusted: clientIP = %q, want RemoteAddr", got)
	}

	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = "127.0.0.1:443" // 受信但未带头（内网直连场景）
	if got := clientIP(req3); got != "127.0.0.1:443" {
		t.Fatalf("trusted without header: clientIP = %q, want RemoteAddr", got)
	}
}

// agents API 的 online 字段：由后端按 AGENT_OFFLINE_AFTER 判定，前端不自算。
func TestPortalAgentsOnlineField(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	if _, err := getDB(); err != nil {
		t.Fatalf("db: %v", err)
	}
	agentStore.Lock()
	agentStore.m["agent-fresh"] = agentState{Heartbeat: agentHeartbeat{AgentID: "agent-fresh"}, LastSeen: time.Now().Add(-5 * time.Second)}
	agentStore.m["agent-stale"] = agentState{Heartbeat: agentHeartbeat{AgentID: "agent-stale"}, LastSeen: time.Now().Add(-10 * time.Minute)}
	agentStore.Unlock()
	t.Cleanup(func() {
		agentStore.Lock()
		delete(agentStore.m, "agent-fresh")
		delete(agentStore.m, "agent-stale")
		agentStore.Unlock()
	})

	req := httptest.NewRequest(http.MethodGet, "/api/portal/agents", nil)
	req = req.WithContext(contextWithUser(req, portalUser{Username: "t", Role: "ops.viewer"}))
	rec := httptest.NewRecorder()
	apiPortalAgents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("agents = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Agents []struct {
			PortalAgentID string `json:"portal_agent_id"`
			Online        bool   `json:"online"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	online := map[string]bool{}
	for _, a := range out.Agents {
		online[a.PortalAgentID] = a.Online
	}
	if !online["agent-fresh"] || online["agent-stale"] {
		t.Fatalf("online = %v, want fresh:true stale:false", online)
	}
}
