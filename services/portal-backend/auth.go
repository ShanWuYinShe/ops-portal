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

// edgeVerified 边缘是否判定证书验证通过。CF 无证请求透传的 X-Cert-Verify
// 是字面量 "false"（见 docs/cf-edge.md），只判非空会把 "false" 当已验证，
// 故显式排除假值（大小写不敏感）。
func edgeVerified(id certIdentity) bool {
	switch strings.ToLower(strings.TrimSpace(id.Verified)) {
	case "", "false", "0", "no":
		return false
	}
	return true
}

// requireCert 要求请求携带受信代理转发的有效证书标识。
// 生产模式双条件：边缘 verified 为真 + serial/指纹命中本地 active 证书库。
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
		if !edgeVerified(id) {
			writeError(w, http.StatusUnauthorized, "client certificate required")
			return
		}
		if findActiveCert(id.Serial, id.Fingerprint) == nil {
			log.Printf("portal: unknown/revoked cert serial=%s fp=%s from %s",
				maskMiddle(id.Serial), maskMiddle(id.Fingerprint), clientIP(r))
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
