package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
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
		ensureServiceKeyPEM()
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
	// 私钥独立 PEM 落盘：portal 主动拨测被管网站 ops 接口时作 mTLS 客户端证书。
	_ = os.WriteFile(serviceKeyPath(), keyPEM, 0600)
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

// ensureServiceKeyPEM 自愈：老版本只写了 .__raw 包与证书 PEM，
// 探测需要独立私钥 PEM 时从包里拆出来补写。
func ensureServiceKeyPEM() {
	if _, err := os.Stat(serviceKeyPath()); err == nil {
		return
	}
	raw, err := os.ReadFile(serviceKeyPath() + ".__raw")
	if err != nil {
		return
	}
	var bundle struct {
		Key  string `json:"key"`
		Cert string `json:"cert"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return
	}
	if bundle.Key == "" {
		return
	}
	_ = os.WriteFile(serviceKeyPath(), []byte(bundle.Key), 0600)
	if _, err := os.Stat(servicePemPath()); err != nil && bundle.Cert != "" {
		_ = os.WriteFile(servicePemPath(), []byte(bundle.Cert), 0600)
	}
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

func listSiteEntries() []siteEntry {
	db, err := getDB()
	if err != nil {
		return []siteEntry{}
	}
	rows, err := db.Query(`SELECT name, ops_url, cert_serial, registered_at FROM sites ORDER BY name`)
	if err != nil {
		return []siteEntry{}
	}
	defer rows.Close()
	out := []siteEntry{}
	for rows.Next() {
		e := siteEntry{}
		if err := rows.Scan(&e.Name, &e.OpsURL, &e.CertSerial, &e.RegisteredAt); err != nil {
			return []siteEntry{}
		}
		out = append(out, e)
	}
	return out
}

func upsertSiteEntry(e siteEntry) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO sites (name, ops_url, cert_serial, registered_at) VALUES (?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET ops_url=excluded.ops_url, cert_serial=excluded.cert_serial,
		registered_at=excluded.registered_at`,
		e.Name, e.OpsURL, e.CertSerial, e.RegisteredAt)
	return err
}

// deleteSiteEntry 删除注册；返回是否删掉过。
func deleteSiteEntry(name string) (bool, error) {
	db, err := getDB()
	if err != nil {
		return false, err
	}
	res, err := db.Exec(`DELETE FROM sites WHERE name=?`, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

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
	entry := siteEntry{Name: name, OpsURL: strings.TrimSpace(body.OpsURL),
		CertSerial: c.Serial, RegisteredAt: time.Now().UTC().Format(time.RFC3339)}
	if err := upsertSiteEntry(entry); err != nil {
		writeInternalError(w, err)
		return
	}
	audit("site:"+name, c.Serial, "site.register", body.OpsURL, "ok", "", r.RemoteAddr)
	writeOK(w, map[string]any{"site": name, "registered": true})
}

// POST /api/portal/sites/unregister（admin）—— 下掉原生注册。
// 只删注册表条目，不影响 agent 心跳视图；该站点的探测结果一并清除。
func apiSitesUnregister(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	u := actorOf(r)
	found, err := deleteSiteEntry(name)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "site not registered")
		return
	}
	clearProbe(name)
	audit(u.Username, "", "site.unregister", name, "ok", "", r.RemoteAddr)
	writeOK(w, map[string]any{"site": name, "unregistered": true})
}

// ── 统一网站视图（「网站」是 portal 的一等实体）──
//
// 两条接入路径汇入同一视图，不复制数据：
//   - agent 心跳（内存实时）：compose 项目 → 该网站部署在哪些机器、容器、git 版本；
//   - 原生注册表（sites 表持久）：网站的 ops 接口元数据（ops_url 等）。
// agent 掉线时网站仍在（带离线标记），原生注册不受 agent 存亡影响。

type siteAgentRef struct {
	AgentID string `json:"portal_agent_id"`
	Online  bool   `json:"online"`
}

// 容器 / git 记录附 agent 归属：容器操作、deploy 都按机器下发，
// 网站部署在多台机器时每条记录各自携带目标 agent。
type siteContainer struct {
	agentContainer
	AgentID string `json:"portal_agent_id"`
}

type siteGitView struct {
	agentSite
	AgentID string `json:"portal_agent_id"`
}

type siteView struct {
	Name       string          `json:"name"`
	Agents     []siteAgentRef  `json:"agents"`
	Containers []siteContainer `json:"containers"`
	Git        []siteGitView   `json:"git"`
	OpsURL     string          `json:"ops_url,omitempty"`
	CertSerial string          `json:"cert_serial,omitempty"`
	// RegisteredAt 仅原生注册的网站有值；agent 发现的网站以心跳为准。
	RegisteredAt string `json:"registered_at,omitempty"`
	// Source: agent（心跳发现）/ native（仅注册表）/ both。
	Source string `json:"source"`
	// Reachable: portal 以 service 证书拨测 ops_url 的结果；
	// nil = 未注册 ops_url 或尚未探测（见 probe.go）。
	Reachable *bool `json:"reachable,omitempty"`
	// Health: 站点自报的健康档（ok|degraded|starting|down），
	// 仅 Reachable 时有效；空串 = 2xx 但响应体不可解析（按 ok 显示）。
	Health    string `json:"health,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}

// agentOnlineAt 与告警看门狗同一判定（AGENT_OFFLINE_AFTER，默认 60s），
// 避免站点视图与 agents 视图对「在线」的口径不一致。
func agentOnlineAt(t time.Time) bool { return time.Since(t) <= agentOfflineAfter() }

// buildSiteViews 按网站名聚合心跳与注册表；名字升序。
func buildSiteViews() ([]siteView, error) {
	entries := listSiteEntries()
	native := make(map[string]siteEntry, len(entries))
	for _, e := range entries {
		native[e.Name] = e
	}

	views := make(map[string]*siteView)
	viewOf := func(name string) *siteView {
		v, ok := views[name]
		if !ok {
			v = &siteView{Name: name, Containers: []siteContainer{}, Git: []siteGitView{}}
			views[name] = v
		}
		return v
	}

	agentStore.Lock()
	for id, st := range agentStore.m {
		ref := siteAgentRef{AgentID: id, Online: agentOnlineAt(st.LastSeen)}
		hb := st.Heartbeat
		seen := make(map[string]bool, len(hb.ComposeProjects))
		ensureAgent := func(v *siteView) {
			if !seen[v.Name] {
				v.Agents = append(v.Agents, ref)
				seen[v.Name] = true
			}
			if v.Source == "" {
				v.Source = "portal-agent"
			}
		}
		for _, p := range hb.ComposeProjects {
			ensureAgent(viewOf(p))
		}
		for _, c := range hb.Containers {
			if c.Project == "" {
				continue
			}
			v := viewOf(c.Project)
			v.Containers = append(v.Containers, siteContainer{agentContainer: c, AgentID: id})
			ensureAgent(v)
		}
		for _, g := range hb.Sites {
			v := viewOf(g.Name)
			v.Git = append(v.Git, siteGitView{agentSite: g, AgentID: id})
			ensureAgent(v)
		}
	}
	agentStore.Unlock()

	for name, e := range native {
		v := viewOf(name)
		v.OpsURL, v.CertSerial, v.RegisteredAt = e.OpsURL, e.CertSerial, e.RegisteredAt
		if v.Source == "portal-agent" {
			v.Source = "both"
		} else {
			v.Source = "native"
		}
	}

	out := make([]siteView, 0, len(views))
	for _, v := range views {
		if v.Agents == nil {
			v.Agents = []siteAgentRef{}
		}
		if v.OpsURL != "" {
			if res, ok := getProbe(v.Name); ok {
				r := res.Reachable
				v.Reachable = &r
				v.Health = res.Health
				v.CheckedAt = res.CheckedAt.UTC().Format(time.RFC3339)
			}
		}
		sort.Slice(v.Containers, func(i, j int) bool {
			return v.Containers[i].Service < v.Containers[j].Service
		})
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GET /api/portal/sites（viewer+）—— 统一网站视图（心跳 + 原生注册合并）。
func apiPortalSites(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	views, err := buildSiteViews()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeOK(w, map[string]any{"sites": views})
}
