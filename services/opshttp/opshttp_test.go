package opshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testCfg() Config {
	return Config{
		Site:          "demo",
		ServiceSerial: "AB12cd34",
		Status: func() Status {
			return Status{
				Containers: []ContainerInfo{{Name: "demo-nginx-1", Service: "nginx", State: "running"}},
				Git:        &GitInfo{Head: "abc", Branch: "main"},
			}
		},
	}
}

func TestVerifyService(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, testCfg())

	// 无证 403
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ops/v1/health", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no cert = %d, want 403", rec.Code)
	}

	// 串号 403
	req := httptest.NewRequest(http.MethodGet, "/ops/v1/health", nil)
	req.Header.Set("X-Cert-Serial", "WRONG")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong serial = %d, want 403", rec.Code)
	}

	// 对号放行（大小写不敏感：CF 透传大写，登记可能是小写）
	for _, s := range []string{"AB12cd34", "ab12CD34"} {
		req := httptest.NewRequest(http.MethodGet, "/ops/v1/health", nil)
		req.Header.Set("X-Cert-Serial", s)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("serial %q = %d, want 200", s, rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["ok"] != true || body["site"] != "demo" {
			t.Fatalf("health body = %s", rec.Body.String())
		}
	}

	// status 快照同构
	req = httptest.NewRequest(http.MethodGet, "/ops/v1/status", nil)
	req.Header.Set("X-Cert-Serial", "AB12cd34")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var st struct {
		OK         bool            `json:"ok"`
		Containers []ContainerInfo `json:"containers"`
		Git        *GitInfo        `json:"git"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.OK {
		t.Fatalf("status body = %s", rec.Body.String())
	}
	if len(st.Containers) != 1 || st.Containers[0].Service != "nginx" || st.Git.Branch != "main" {
		t.Fatalf("status = %+v", st)
	}

	// 非 GET 由 mux 直接 405
	req = httptest.NewRequest(http.MethodPost, "/ops/v1/health", nil)
	req.Header.Set("X-Cert-Serial", "AB12cd34")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post = %d, want 405", rec.Code)
	}
}
