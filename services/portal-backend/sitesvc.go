package main

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 证书分类（kind 字段，严格分域、互不混用）：
//   human   —— 人登录 ops portal（浏览器设备证书）
//   agent   —— 被管机 ops-agent → ops（机器证书 + bearer token 双因子）
//   service —— ops 服务端自己的客户端证书：portal 主动调用被管网站的
//              ops 管理接口时出示；所有被管网站放行这张证书（按 serial 钉死）。
//   site    —— portal 签发给被管网站的注册证书：网站以此访问 ops 注册 API。

// ── service 证书（portal 服务端客户端证书）──

func serviceKeyPath() string { return dataDir() + "/service-client.key" }
func servicePemPath() string { return dataDir() + "/service-client.pem" }

var serviceMu sync.Mutex

// POST /api/portal/service-cert/issue（admin）
// 幂等：已有 active service 证书则直接返回既有公钥材料（私钥只生成一次，
// 持久化在服务器 data 目录，供 portal 出网调用被管网站使用）。
func apiServiceCertIssue(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	serviceMu.Lock()
	defer serviceMu.Unlock()
	u := actorOf(r)

	if existing := findServiceCert(); existing != nil {
		pemOut, _ := os.ReadFile(servicePemPath())
		writeOK(w, map[string]any{
			"cert_id": existing.ID, "serial": existing.Serial,
			"fingerprint": existing.Fingerprint, "expires_at": existing.ExpiresAt,
			"client_cert": string(pemOut), "note": "复用既有 service 证书（serial 需在被管网站放行清单钉死）",
		})
		return
	}

	keyPEM, csrPEM, err := genCSR("ops-portal-service")
	if err != nil {
		writeInternalError(w, err)
		return
	}
	certPEM, cfID, serial, fp, expires, err := cfIssueClientCert(csrPEM)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	c := certRecord{
		ID: "cert-service-" + randHex(4), Label: "ops-portal-service", Kind: "service",
		CFID: cfID, Serial: serial, Fingerprint: fp, CommonName: "ops-portal-service",
		Issuer: "cloudflare-managed", Status: "active", ExpiresAt: expires,
		Note: "portal 服务端客户端证书：被管网站的 ops 接口放行清单钉死此 serial",
	}
	if _, err := addCert(c); err != nil {
		writeInternalError(w, err)
		return
	}
	if err := atomicWriteJSON(serviceKeyPath()+".__raw", map[string]any{
		"key": string(keyPEM), "cert": string(certPEM),
	}, 0600); err != nil {
		writeInternalError(w, err)
		return
	}
	_ = os.WriteFile(servicePemPath(), certPEM, 0600)
	audit(u.Username, serial, "service_cert.issue", c.ID, "ok", "被管网站放行清单用", r.RemoteAddr)
	writeOK(w, map[string]any{
		"cert_id": c.ID, "serial": serial, "fingerprint": fp,
		"expires_at": expires, "client_cert": string(certPEM),
		"note": "serial " + serial + " 须写入每个被管网站 ops 接口的放行清单",
	})
}

func findServiceCert() *certRecord {
	for _, c := range listCerts() {
		if c.Kind == "service" && c.Status == "active" {
			return &c
		}
	}
	return nil
}

// ── site 注册证书 + 站点注册表 ──

type siteEntry struct {
	Name         string `json:"name"`
	OpsURL       string `json:"ops_url"`
	CertSerial   string `json:"cert_serial"`
	RegisteredAt string `json:"registered_at"`
}

type siteRegistry struct {
	Sites []siteEntry `json:"sites"`
}

var sitesMu sync.Mutex

func sitesPath() string { return dataDir() + "/sites.json" }

func loadSites() (*siteRegistry, error) {
	s := &siteRegistry{}
	if err := readJSON(sitesPath(), s); err != nil {
		if os.IsNotExist(err) {
			return &siteRegistry{}, nil
		}
		return nil, err
	}
	if s.Sites == nil {
		s.Sites = []siteEntry{}
	}
	return s, nil
}

func saveSites(s *siteRegistry) error { return atomicWriteJSON(sitesPath(), s, 0600) }

// POST /api/portal/site-certificates/issue（admin）
// 为被管网站签发注册证书（kind=site），bundle 一次下发：证书+私钥+说明。
func apiSiteCertificateIssue(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		SiteName string `json:"site_name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	site := strings.TrimSpace(body.SiteName)
	if site == "" {
		writeError(w, http.StatusBadRequest, "site_name required")
		return
	}
	u := actorOf(r)
	keyPEM, csrPEM, err := genCSR("site-" + site)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	certPEM, cfID, serial, fp, expires, err := cfIssueClientCert(csrPEM)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	c := certRecord{
		ID: "cert-site-" + randHex(4), Label: site, Kind: "site",
		CFID: cfID, Serial: serial, Fingerprint: fp, CommonName: "site-" + site,
		Issuer: "cloudflare-managed", Status: "active", ExpiresAt: expires,
		Note: "被管网站注册证书：仅可访问 /api/site/*",
	}
	if _, err := addCert(c); err != nil {
		writeInternalError(w, err)
		return
	}
	audit(u.Username, serial, "site_cert.issue", site, "ok", "", r.RemoteAddr)
	writeOK(w, map[string]any{
		"site_name": site, "serial": serial, "fingerprint": fp,
		"expires_at": expires, "client_cert": string(certPEM), "client_key": string(keyPEM),
		"note": "证书包仅此一次返回；网站仅可调用 /api/site/*，不可访问管理面",
	})
}

// POST /api/site/register —— 被管网站自助注册（须出示 kind=site 证书）。
func apiSiteRegister(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	id := certFromRequest(r)
	c := findActiveCert(id.Serial, id.Fingerprint)
	if c == nil || c.Kind != "site" {
		writeError(w, http.StatusForbidden, "site certificate required")
		return
	}
	var body struct {
		Name   string `json:"name"`
		OpsURL string `json:"ops_url"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || c.Label != name {
		writeError(w, http.StatusForbidden, "site name mismatch with certificate")
		return
	}
	sitesMu.Lock()
	s, err := loadSites()
	if err != nil {
		sitesMu.Unlock()
		writeInternalError(w, err)
		return
	}
	entry := siteEntry{Name: name, OpsURL: strings.TrimSpace(body.OpsURL),
		CertSerial: c.Serial, RegisteredAt: time.Now().UTC().Format(time.RFC3339)}
	replaced := false
	for i := range s.Sites {
		if s.Sites[i].Name == name {
			s.Sites[i] = entry
			replaced = true
		}
	}
	if !replaced {
		s.Sites = append(s.Sites, entry)
	}
	err = saveSites(s)
	sitesMu.Unlock()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	audit("site:"+name, c.Serial, "site.register", body.OpsURL, "ok", "", r.RemoteAddr)
	writeOK(w, map[string]any{"site": name, "registered": true})
}

// GET /api/portal/sites（admin）—— 已注册站点清单。
func apiPortalSites(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	s, err := loadSites()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeOK(w, map[string]any{"sites": s.Sites})
}
