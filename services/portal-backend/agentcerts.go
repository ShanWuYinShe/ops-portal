package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 机器证书：agent 与人一样持 CF 托管 CA 客户端证书（kind=portal-agent），
// 边缘 WAF 不再豁免 /api/portal-agent/*，机器请求同样过边缘 mTLS；
// origin 侧校验「证书在表且 kind=portal-agent + bearer token 同 portal_agent_id」双因子。

// POST /api/portal/agent-certificates/issue（admin）
// 生成密钥对+CSR → 调 CF API 签发 → 入证书表 → 一次性返回机器证书包。
func apiAgentCertificateIssue(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		AgentID string `json:"portal_agent_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	agentID := strings.TrimSpace(body.AgentID)
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "portal_agent_id required")
		return
	}
	u := actorOf(r)

	key, csrPEM, err := genCSR(agentID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	certPEM, cfID, serial, fp, expires, err := cfIssueClientCert(csrPEM)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if _, err := addCert(certRecord{
		ID: "cert-portal-agent-" + randHex(6), Label: agentID, Kind: "portal-agent",
		CFID: cfID, Serial: serial, Fingerprint: fp, CommonName: agentID,
		Issuer: "cloudflare-managed", Status: "active",
		ExpiresAt: expires, Note: "机器证书（portal-agent）",
	}); err != nil {
		writeInternalError(w, err)
		return
	}
	tokenPlain, tok, err := issueAgentToken(agentID, u.Username)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	audit(u.Username, serial, "portal_agent_cert.issue", agentID, "ok", tok.ID, r.RemoteAddr)
	writeOK(w, map[string]any{
		"portal_agent_id": agentID, "client_cert": string(certPEM),
		"client_key": string(key), "portal_agent_token": tokenPlain,
		"serial": serial, "fingerprint": fp, "expires_at": expires,
		"note": "证书包仅此一次返回；portal-agent 侧以客户端证书出网 + bearer token 双因子",
	})
}

func genCSR(cn string) (keyPEM, csrPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	tpl := x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: cn, Organization: []string{"portal-agent"}},
		SignatureAlgorithm: x509.SHA256WithRSA,
	}
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, &tpl, key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer})
	return keyPEM, csrPEM, nil
}

func cfZoneID() string   { return getEnv("CF_ZONE_ID", "") }
func cfAPIToken() string { return getEnv("CF_API_TOKEN", "") }

// cfAPIClient 带 30s 整体超时：CF API 挂起不能拖死签发请求（admin handler 同步等待）。
var cfAPIClient = &http.Client{Timeout: 30 * time.Second}

func cfIssueClientCert(csrPEM []byte) (certPEM []byte, cfID, serial, fp, expires string, err error) {
	if cfAPIToken() == "" || cfZoneID() == "" {
		return nil, "", "", "", "", errors.New("CF_API_TOKEN/CF_ZONE_ID not configured")
	}
	csr := strings.ReplaceAll(string(csrPEM), "\n", "\\n")
	body := fmt.Sprintf(`{"validity_days":825,"csr":"%s"}`, csr)
	req, _ := http.NewRequest("POST",
		"https://api.cloudflare.com/client/v4/zones/"+cfZoneID()+"/client_certificates",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+cfAPIToken())
	req.Header.Set("Content-Type", "application/json")
	resp, err := cfAPIClient.Do(req)
	if err != nil {
		return nil, "", "", "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Success bool `json:"success"`
		Result  struct {
			ID                string `json:"id"`
			SerialNumber      string `json:"serial_number"`
			FingerprintSHA256 string `json:"fingerprint_sha256"`
			ExpiresOn         string `json:"expires_on"`
			Certificate       string `json:"certificate"`
		} `json:"result"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", "", "", "", err
	}
	if !out.Success {
		return nil, "", "", "", "", fmt.Errorf("CF API: %v", out.Errors)
	}
	return []byte(out.Result.Certificate), out.Result.ID, out.Result.SerialNumber,
		out.Result.FingerprintSHA256, out.Result.ExpiresOn, nil
}
