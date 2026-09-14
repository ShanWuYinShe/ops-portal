// Package opshttp 是被管网站原生实现 /ops/v1/* 的参考包
// （约定见 docs/ops-v1-spec.md）。标准库单文件无依赖：拷走即用，
// 各站点不要重写验签逻辑。
//
// 用法（被管网站 main 里）：
//
//	mux := http.NewServeMux()
//	opshttp.Register(mux, opshttp.Config{
//		Site:          "hotspot",
//		ServiceSerial: "197FE164…", // portal service 证书 serial（放行清单）
//		Status: func() opshttp.Status {
//			return opshttp.Status{Containers: myContainers(), Git: myGit()}
//		},
//	})
package opshttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Config 放行配置：ServiceSerial 为 portal 下发的 service 证书 serial。
// CertSerialHeader 与 CF 边缘透传头对齐，默认 X-Cert-Serial。
type Config struct {
	Site             string
	ServiceSerial    string
	CertSerialHeader string
	Status           func() Status
}

func (c Config) serialHeader() string {
	if c.CertSerialHeader != "" {
		return c.CertSerialHeader
	}
	return "X-Cert-Serial"
}

// ContainerInfo 与 portal agent 心跳的容器快照同构（见 ops-v1-spec.md）。
type ContainerInfo struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	State   string `json:"state"`
	Status  string `json:"status,omitempty"`
	Image   string `json:"image,omitempty"`
}

// GitInfo 站点版本快照。
type GitInfo struct {
	Head   string `json:"head,omitempty"`
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty,omitempty"`
}

// Status /ops/v1/status 的业务部分（ok/site/time 由 handler 补）。
type Status struct {
	Containers []ContainerInfo `json:"containers,omitempty"`
	Git        *GitInfo        `json:"git,omitempty"`
}

// VerifyService 鉴权中间件：仅放行出示 portal service 证书的调用方
// （边缘透传的 serial 钉死比对，大小写不敏感；其余一律 403）。
func VerifyService(cfg Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(r.Header.Get(cfg.serialHeader()))
		if got == "" || cfg.ServiceSerial == "" || !strings.EqualFold(got, cfg.ServiceSerial) {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	}
}

func writeOK(w http.ResponseWriter, v map[string]any) {
	v["ok"] = true
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

// health /ops/v1/health 存活端点。
func health(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeOK(w, map[string]any{
			"site": cfg.Site,
			"time": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// status /ops/v1/status 快照端点（数据由站点回调查）。
func status(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		st := Status{}
		if cfg.Status != nil {
			st = cfg.Status()
		}
		if st.Containers == nil {
			st.Containers = []ContainerInfo{}
		}
		writeOK(w, map[string]any{
			"site":       cfg.Site,
			"time":       time.Now().UTC().Format(time.RFC3339),
			"containers": st.Containers,
			"git":        st.Git,
		})
	}
}

// Register 把两端点（套鉴权）挂到 mux。
func Register(mux *http.ServeMux, cfg Config) {
	mux.HandleFunc("GET /ops/v1/health", VerifyService(cfg, health(cfg)))
	mux.HandleFunc("GET /ops/v1/status", VerifyService(cfg, status(cfg)))
}
