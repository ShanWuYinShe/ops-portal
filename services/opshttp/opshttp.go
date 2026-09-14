// Package opshttp 是被管网站实现 GET /ops/v1/health 的参考包
// （约定见 docs/ops-v1-spec.md）。标准库单文件无依赖：拷走即用，
// 各站点不要重写验签逻辑。
//
// 用法（被管网站 main 里）：
//
//	mux := http.NewServeMux()
//	opshttp.Register(mux, opshttp.Config{
//		Site:          "hotspot",
//		Version:       "1.2.3",
//		ServiceSerial: "197FE164…", // portal service 证书 serial（放行清单）
//		Health: func(ctx context.Context) opshttp.HealthReport {
//			return opshttp.HealthReport{Checks: []opshttp.Check{
//				myPostgresCheck(ctx),
//				myRedisCheck(ctx),
//			}}
//		},
//	})
//
// 注意：Health 必须尊重 ctx（超时/取消时立即返回），
// 否则超时的检查 goroutine 会堆积。
package opshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Config 放行与健康配置：ServiceSerial 为 portal 下发的 service 证书 serial。
// CertSerialHeader 与 CF 边缘透传头对齐，默认 X-Cert-Serial。
// Timeout 为健康端点整体上限，默认 5s（spec 要求站点 ≤ 1s，此处留余量）。
type Config struct {
	Site             string
	Version          string
	Commit           string
	ServiceSerial    string
	CertSerialHeader string
	Timeout          time.Duration
	Health           func(ctx context.Context) HealthReport
}

// Check 单项依赖检查结果（由站点实现并计时）。
type Check struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
}

// HealthReport 站点自判的健康报告。Status 为空时由 checks 推导
// （全 ok → "ok"，否则 "degraded"）；非法值归一为 "degraded"。
type HealthReport struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
}

func (c Config) serialHeader() string {
	if c.CertSerialHeader != "" {
		return c.CertSerialHeader
	}
	return "X-Cert-Serial"
}

func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 5 * time.Second
}

// normalizeStatus 归一状态：空则推导，非法则降级。大小写不敏感，输出小写。
func normalizeStatus(status string, checks []Check) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "degraded", "starting":
		return strings.ToLower(strings.TrimSpace(status))
	case "":
		for _, ch := range checks {
			if !ch.OK {
				return "degraded"
			}
		}
		return "ok"
	default:
		return "degraded"
	}
}

// VerifyService 鉴权中间件：仅放行出示 portal service 证书的调用方
// （边缘透传的 serial 钉死比对，大小写不敏感；其余一律 403，
// 配置缺失时拒绝一切请求）。
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

func writeJSON(w http.ResponseWriter, status int, v map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}

// health GET /ops/v1/health。检查函数 panic 时兜底为 degraded（200），
// 绝不因检查自身的崩溃返回 500。
func health(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rep HealthReport
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					// 注意：嵌套字面量的右括号必须同行闭合（}}})，
					// 否则换行触发分号插入导致编译失败。
					rep = HealthReport{Status: "degraded", Checks: []Check{{
						Name: "health-panic", Detail: "checker panic (see site logs)",
					}}}
				}
			}()
			if cfg.Health != nil {
				rep = cfg.Health(r.Context())
			}
		}()
		status := normalizeStatus(rep.Status, rep.Checks)
		checks := rep.Checks
		if checks == nil {
			checks = []Check{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"site":    cfg.Site,
			"status":  status,
			"time":    time.Now().UTC().Format(time.RFC3339),
			"version": cfg.Version,
			"commit":  cfg.Commit,
			"checks":  checks,
		})
	}
}

// timeoutBody 超时兜底响应体。超时 = 答不上来，按契约记为不可用。
func timeoutBody(cfg Config) string {
	b, _ := json.Marshal(map[string]any{
		"ok":     false,
		"site":   cfg.Site,
		"status": "down",
		"time":   time.Now().UTC().Format(time.RFC3339),
		"error":  "health timeout",
	})
	return string(b)
}

// Register 只挂一个端点：GET /ops/v1/health。
// POST 等其他方法由 mux 直接 405（spec：健康检查只接受 GET，无例外）。
func Register(mux *http.ServeMux, cfg Config) {
	h := VerifyService(cfg, health(cfg))
	mux.HandleFunc("GET /ops/v1/health", http.TimeoutHandler(h, cfg.timeout(), timeoutBody(cfg)).ServeHTTP)
}
