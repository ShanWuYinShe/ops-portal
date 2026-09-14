package opshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testCfg() Config {
	return Config{
		Site:          "demo",
		Version:       "1.2.3",
		Commit:        "abc123",
		ServiceSerial: "AB12cd34",
		Health: func(ctx context.Context) HealthReport {
			return HealthReport{Checks: []Check{
				{Name: "postgres", OK: true, LatencyMs: 3},
			}}
		},
	}
}

func get(t *testing.T, mux http.Handler, serial string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ops/v1/health", nil)
	if serial != "" {
		req.Header.Set("X-Cert-Serial", serial)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeHealth(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %s", rec.Body.String())
	}
	return body
}

func TestVerifyService(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, testCfg())

	// 无证 403
	if rec := get(t, mux, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("no cert = %d, want 403", rec.Code)
	}
	// 串号 403
	if rec := get(t, mux, "WRONG"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong serial = %d, want 403", rec.Code)
	}
	// 对号放行（大小写不敏感：CF 透传大写，登记可能是小写）
	for _, s := range []string{"AB12cd34", "ab12CD34"} {
		if rec := get(t, mux, s); rec.Code != http.StatusOK {
			t.Fatalf("serial %q = %d, want 200", s, rec.Code)
		}
	}
	// 配置缺失 fail-closed：serial 为空时拒绝一切请求
	broken := testCfg()
	broken.ServiceSerial = ""
	mux2 := http.NewServeMux()
	Register(mux2, broken)
	if rec := get(t, mux2, "AB12cd34"); rec.Code != http.StatusForbidden {
		t.Fatalf("empty config serial = %d, want 403", rec.Code)
	}
}

func TestHealthBody(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, testCfg())
	body := decodeHealth(t, get(t, mux, "AB12cd34"))
	if body["ok"] != true || body["site"] != "demo" || body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
	if body["version"] != "1.2.3" || body["commit"] != "abc123" {
		t.Fatalf("version/commit = %v", body)
	}
	checks, _ := body["checks"].([]any)
	if len(checks) != 1 || checks[0].(map[string]any)["name"] != "postgres" {
		t.Fatalf("checks = %v", checks)
	}
	if _, ok := body["time"]; !ok {
		t.Fatalf("missing time: %v", body)
	}
}

func TestStatusDerivation(t *testing.T) {
	cases := []struct {
		name   string
		report HealthReport
		want   string
	}{
		{"显式 ok", HealthReport{Status: "ok"}, "ok"},
		{"显式 degraded", HealthReport{Status: "degraded"}, "degraded"},
		{"显式 starting", HealthReport{Status: "starting"}, "starting"},
		{"大小写不敏感", HealthReport{Status: "Degraded"}, "degraded"},
		{"空则全 ok 推导", HealthReport{Checks: []Check{{Name: "a", OK: true}}}, "ok"},
		{"空则有坏推导", HealthReport{Checks: []Check{{Name: "a", OK: true}, {Name: "b", OK: false}}}, "degraded"},
		{"空且无 checks 推导 ok", HealthReport{}, "ok"},
		{"非法值归一降级", HealthReport{Status: "whatever"}, "degraded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Health = func(ctx context.Context) HealthReport { return c.report }
			mux := http.NewServeMux()
			Register(mux, cfg)
			body := decodeHealth(t, get(t, mux, "AB12cd34"))
			if body["status"] != c.want {
				t.Fatalf("status = %v, want %s", body["status"], c.want)
			}
		})
	}
}

func TestCheckerPanicDegraded(t *testing.T) {
	cfg := testCfg()
	cfg.Health = func(ctx context.Context) HealthReport { panic("boom") }
	mux := http.NewServeMux()
	Register(mux, cfg)
	rec := get(t, mux, "AB12cd34")
	if rec.Code != http.StatusOK {
		t.Fatalf("panic = %d, want 200 (degraded, not 500)", rec.Code)
	}
	body := decodeHealth(t, rec)
	if body["status"] != "degraded" {
		t.Fatalf("status = %v, want degraded", body["status"])
	}
	checks, _ := body["checks"].([]any)
	if len(checks) != 1 || checks[0].(map[string]any)["name"] != "health-panic" {
		t.Fatalf("checks = %v", checks)
	}
}

// POST 返回 405：健康检查只接受 GET，无例外、无双动词兼容。
func TestPostRejected(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, testCfg())
	req := httptest.NewRequest(http.MethodPost, "/ops/v1/health", strings.NewReader(`{}`))
	req.Header.Set("X-Cert-Serial", "AB12cd34")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post = %d, want 405", rec.Code)
	}
}

func TestTimeout(t *testing.T) {
	cfg := testCfg()
	cfg.Timeout = 50 * time.Millisecond
	cfg.Health = func(ctx context.Context) HealthReport {
		<-ctx.Done()
		return HealthReport{Status: "ok"}
	}
	mux := http.NewServeMux()
	Register(mux, cfg)
	rec := get(t, mux, "AB12cd34")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("timeout = %d, want 503", rec.Code)
	}
}
