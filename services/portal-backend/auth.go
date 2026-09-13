package main

import (
	"log"
	"net/http"
	"strings"
	"sync"
)

// 证书门禁（Phase 1：认头 + 记账；Phase 2：查有效证书表 + RBAC）。
//
// 信任链：Cloudflare 边缘验客户端证书 → Transform Rules 注入 X-Cert-* 头
// → Origin Pulls 保证只有 CF 能连源站 → 本文件只认受信代理的头。
// 本地 dev（DEV_ALLOW_NO_CERT=1）允许无头访问 whoami，仅限本机联调。

type certIdentity struct {
	Verified    string
	Serial      string
	Subject     string
	Fingerprint string
	Trusted     bool // 头来自受信代理
}

var (
	certVerifyHeader     = "X-Cert-Verify"
	certSerialHeader     = "X-Cert-Serial"
	certSubjectHeader    = "X-Cert-Subject"
	certFingerprintLabel = "X-Cert-Fingerprint"
	headerInitOnce       sync.Once
)

func initCertHeaders() {
	headerInitOnce.Do(func() {
		if v := strings.TrimSpace(getEnv("CERT_VERIFY_HEADER", "")); v != "" {
			certVerifyHeader = v
		}
		if v := strings.TrimSpace(getEnv("CERT_SERIAL_HEADER", "")); v != "" {
			certSerialHeader = v
		}
		if v := strings.TrimSpace(getEnv("CERT_SUBJECT_HEADER", "")); v != "" {
			certSubjectHeader = v
		}
		if v := strings.TrimSpace(getEnv("CERT_FINGERPRINT_HEADER", "")); v != "" {
			certFingerprintLabel = v
		}
	})
}

func certFromRequest(r *http.Request) certIdentity {
	initCertHeaders()
	id := certIdentity{
		Verified:    r.Header.Get(certVerifyHeader),
		Serial:      r.Header.Get(certSerialHeader),
		Subject:     r.Header.Get(certSubjectHeader),
		Fingerprint: r.Header.Get(certFingerprintLabel),
		Trusted:     isTrustedProxy(r),
	}
	return id
}

// requireCert 要求请求携带受信代理转发的有效证书标识。
// 生产模式双条件：边缘 verified 非空 + serial/指纹命中本地 active 证书库。
// 吊销即本地 revoke（边缘 WAF 规则另行同步，见 docs/cf-edge.md）。
func requireCert(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := certFromRequest(r)
		if !id.Trusted {
			log.Printf("portal: untrusted proxy attempt from %s", r.RemoteAddr)
			writeError(w, http.StatusForbidden, "untrusted proxy")
			return
		}
		if getEnv("DEV_ALLOW_NO_CERT", "") != "" {
			next(w, r)
			return
		}
		if strings.TrimSpace(id.Verified) == "" {
			writeError(w, http.StatusUnauthorized, "client certificate required")
			return
		}
		if findActiveCert(id.Serial, id.Fingerprint) == nil {
			log.Printf("portal: unknown/revoked cert serial=%s fp=%s from %s",
				maskMiddle(id.Serial), maskMiddle(id.Fingerprint), r.RemoteAddr)
			writeError(w, http.StatusForbidden, "certificate not authorized")
			return
		}
		next(w, r)
	}
}

// maskMiddle 日志脱敏：留首尾各 6 字符。
func maskMiddle(s string) string {
	if len(s) <= 12 {
		return "***"
	}
	return s[:6] + "..." + s[len(s)-6:]
}

// requireAgentToken 要求 agent 请求携带预共享 token。
// agent 走独立的 /api/agent/* 树，与人用的证书门禁分离。
func requireAgentToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := strings.TrimSpace(getEnv("AGENT_TOKEN", ""))
		if want == "" {
			writeError(w, http.StatusServiceUnavailable, "agent token not configured")
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if got == "" || !constantTimeEqual(got, want) {
			writeError(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		next(w, r)
	}
}
