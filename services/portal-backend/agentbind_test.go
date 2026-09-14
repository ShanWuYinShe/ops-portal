package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// token-agent 身份绑定：持 A 的 token 上报 B 的心跳/回传必须 403；
// 上报自己放行；legacy 单 token 回退不绑定（dev 兼容）。
func TestAgentBindingEnforced(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	t.Setenv("PORTAL_AGENT_TOKEN", "")

	plainA, _, err := issueAgentToken("agent-a-01", "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	post := func(token, body string, h http.HandlerFunc) int {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.RemoteAddr = "192.0.2.9:1234" // 非受信代理：跳过机器证书分支
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}
	hb := requireAgentTokenV2(apiAgentHeartbeat)
	rep := requireAgentTokenV2(apiAgentReport)

	mismatch := `{"portal_agent_id":"agent-b-01","hostname":"h","compose_projects":[],"sites":[],"containers":[]}`
	if code := post(plainA, mismatch, hb); code != http.StatusForbidden {
		t.Fatalf("heartbeat mismatch = %d, want 403", code)
	}
	if code := post(plainA, `{"portal_agent_id":"agent-b-01","reports":[]}`, rep); code != http.StatusForbidden {
		t.Fatalf("report mismatch = %d, want 403", code)
	}

	match := `{"portal_agent_id":"agent-a-01","hostname":"h","compose_projects":[],"sites":[],"containers":[]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(match))
	req.RemoteAddr = "192.0.2.9:1234"
	req.Header.Set("Authorization", "Bearer "+plainA)
	hb(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat match = %d, want 200", rec.Code)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatalf("heartbeat body = %s", rec.Body.String())
	}

	// legacy 回退：PORTAL_AGENT_TOKEN 生效时不绑定，上报任意 portal_agent_id 放行
	t.Setenv("PORTAL_AGENT_TOKEN", "dev-legacy-secret")
	if code := post("dev-legacy-secret", mismatch, hb); code != http.StatusOK {
		t.Fatalf("legacy heartbeat = %d, want 200", code)
	}
}
