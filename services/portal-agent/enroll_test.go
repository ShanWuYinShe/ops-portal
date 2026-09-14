package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// 自注册：无 AGENT_TOKEN 时用 AGENT_ENROLL_TOKEN 兑换，成功后落盘；
// 重启优先读盘；兑换失败返回错误。
func TestResolveTokenEnroll(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/portal-agent/enroll" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"token":"opa_test123"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("PORTAL_AGENT_TOKEN", "")
	t.Setenv("PORTAL_AGENT_ENROLL_TOKEN", "enroll_test123")
	t.Setenv("PORTAL_AGENT_TOKEN_FILE", filepath.Join(dir, "agent-token"))

	tok, err := resolveToken(context.Background(), srv.URL, "agent-test-01")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tok != "opa_test123" || calls != 1 {
		t.Fatalf("tok=%q calls=%d", tok, calls)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agent-token"))
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if string(data) != "opa_test123\n" {
		t.Fatalf("file = %q", data)
	}

	// 重启：读盘，不再调兑换接口
	t.Setenv("PORTAL_AGENT_ENROLL_TOKEN", "enroll_used_up")
	tok2, err := resolveToken(context.Background(), srv.URL, "agent-test-01")
	if err != nil || tok2 != "opa_test123" || calls != 1 {
		t.Fatalf("cached tok=%q err=%v calls=%d", tok2, err, calls)
	}

	// AGENT_TOKEN 优先于一切
	t.Setenv("PORTAL_AGENT_TOKEN", "opa_direct")
	tok3, err := resolveToken(context.Background(), srv.URL, "agent-test-01")
	if err != nil || tok3 != "opa_direct" {
		t.Fatalf("direct tok=%q err=%v", tok3, err)
	}

	// 兑换失败：透传错误
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid enroll token"}`))
	}))
	defer bad.Close()
	t.Setenv("PORTAL_AGENT_TOKEN", "")
	t.Setenv("PORTAL_AGENT_TOKEN_FILE", filepath.Join(dir, "missing"))
	if _, err := resolveToken(context.Background(), bad.URL, "agent-test-01"); err == nil {
		t.Fatal("bad enroll should fail")
	}
}
