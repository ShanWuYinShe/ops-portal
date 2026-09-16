package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 边缘验证头语义：CF 无证请求透传的 X-Cert-Verify 是字面量 "false"，
// 只判非空会把 "false" 当已验证（见 docs/cf-edge.md）。
func TestEdgeVerifiedFalseValues(t *testing.T) {
	cases := map[string]bool{
		"true":   true,
		"TRUE":   true,
		" ok ":   true,
		"":       false,
		"false":  false,
		"False":  false,
		"0":      false,
		"no":     false,
		" false": false,
	}
	for in, want := range cases {
		if got := edgeVerified(certIdentity{Verified: in}); got != want {
			t.Fatalf("edgeVerified(%q) = %v, want %v", in, got, want)
		}
	}
}

// 门禁纵深：verify 头为 "false" 时即便证书在表也不放行（边缘 WAF 之外的第二道）。
func TestRequireCertRejectsFalseVerify(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	t.Setenv("DEV_ALLOW_NO_CERT", "")
	seedCert(t, "cert-false-test", "liyou-mbpro")

	next := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	h := requireCert(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:5555" // 受信代理
	req.Header.Set(certVerifyHeader, "false")
	req.Header.Set(certSerialHeader, "S-cert-false-test")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("verify=false with valid serial = %d, want 401", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "127.0.0.1:5555"
	req2.Header.Set(certVerifyHeader, "true")
	req2.Header.Set(certSerialHeader, "S-cert-false-test")
	rec2 := httptest.NewRecorder()
	h(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("verify=true with valid serial = %d, want 200", rec2.Code)
	}
}
