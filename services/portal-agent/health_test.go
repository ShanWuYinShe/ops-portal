package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func resetHeartbeat() {
	lastBeatAt.Store(0)
	lastBeatOK.Store(false)
}

func TestHealthHandlerNeverBeat(t *testing.T) {
	t.Cleanup(resetHeartbeat)
	resetHeartbeat()
	rec := httptest.NewRecorder()
	healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"last_heartbeat":"never"`) {
		t.Fatalf("body = %s, want ok:true + never", body)
	}
}

func TestHealthHandlerAfterHeartbeat(t *testing.T) {
	t.Cleanup(resetHeartbeat)
	resetHeartbeat()
	recordHeartbeat(nil)
	rec := httptest.NewRecorder()
	healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"ok":true}}`) {
		t.Fatalf("body = %s, want last_heartbeat ok:true", body)
	}

	recordHeartbeat(errHeartbeatRejected)
	rec = httptest.NewRecorder()
	healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("心跳失败后仍须 200（活性与连通性分工），got %d", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, `"ok":false`) {
		t.Fatalf("body = %s, want last_heartbeat ok:false", body)
	}
}

func TestStartHealthServerDisabled(t *testing.T) {
	// 空串 = 禁用：不得 panic、不得监听。
	startHealthServer("")
}

func TestStartHealthServerServesHealth(t *testing.T) {
	// 端口 0 = 由内核分配，规避测试并发端口冲突。
	srv, ln, err := newHealthServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newHealthServer: %v", err)
	}
	// 测试生命周期内临时 server，关闭错误无意义。
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	addr := ln.Addr().String()

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// 非 /health 路径不得应答（ServeMux 404）
	resp2, err := http.Get("http://" + addr + "/other")
	if err != nil {
		t.Fatalf("GET /other: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("/other status = %d, want 404", resp2.StatusCode)
	}
}
