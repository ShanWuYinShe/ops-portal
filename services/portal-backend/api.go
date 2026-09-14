package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// 证书 / 用户 / agent token / 命令 / 审计 API。
// 读：ops.viewer+；写：ops.admin（命令入队 ops.operator+）。

func mustMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func actorOf(r *http.Request) portalUser { return userFromContext(r) }

// --- 证书 ---

func apiCertsList(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	writeOK(w, map[string]any{"certs": listCerts()})
}

func apiCertsAdd(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var c certRecord
	if !decodeBody(w, r, &c) {
		return
	}
	if strings.TrimSpace(c.Label) == "" || strings.TrimSpace(c.Serial) == "" {
		writeError(w, http.StatusBadRequest, "label and serial required")
		return
	}
	if c.ID == "" {
		c.ID = "cert-" + randHex(6)
	}
	if c.Issuer == "" {
		c.Issuer = "cloudflare-managed"
	}
	u := actorOf(r)
	saved, err := addCert(c)
	if err != nil {
		audit(u.Username, "", "certs.register", c.ID, "fail", err.Error(), r.RemoteAddr)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	audit(u.Username, saved.Serial, "certs.register", saved.ID, "ok", saved.Label, r.RemoteAddr)
	writeOK(w, map[string]any{"cert": saved})
}

func apiCertsRevoke(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	c, err := revokeCert(body.ID)
	if err != nil {
		audit(u.Username, "", "certs.revoke", body.ID, "fail", err.Error(), r.RemoteAddr)
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// 注意：本地 revoke 后必须同步去 CF 面板/调 CF API 吊销该证（见 docs/cf-edge.md §6），
	// 否则边缘仍放行。此处记审计提醒操作人。
	audit(u.Username, c.Serial, "certs.revoke", c.ID, "ok", "还需同步到 CF 吊销", r.RemoteAddr)
	writeOK(w, map[string]any{"cert": c, "cf_sync_required": true})
}

// --- agent token ---

func apiAgentTokens(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	toks := listAgentTokens()
	writeOK(w, map[string]any{"tokens": toks})
}

func apiAgentTokensIssue(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		AgentID string `json:"agent_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	plain, t, err := issueAgentToken(strings.TrimSpace(body.AgentID), u.Username)
	if err != nil {
		audit(u.Username, "", "agent_tokens.issue", body.AgentID, "fail", err.Error(), r.RemoteAddr)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	audit(u.Username, "", "agent_tokens.issue", t.ID, "ok", "agent="+t.AgentID, r.RemoteAddr)
	// 明文只返回这一次
	writeOK(w, map[string]any{"token": plain, "id": t.ID, "agent_id": t.AgentID})
}

func apiAgentTokensRevoke(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	if err := revokeAgentToken(body.ID); err != nil {
		audit(u.Username, "", "agent_tokens.revoke", body.ID, "fail", err.Error(), r.RemoteAddr)
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	audit(u.Username, "", "agent_tokens.revoke", body.ID, "ok", "", r.RemoteAddr)
	writeOK(w, nil)
}

// --- 命令 ---

func apiCommandsList(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	writeOK(w, map[string]any{"commands": listCommands(r.URL.Query().Get("agent_id"))})
}

func apiCommandsEnqueue(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		AgentID string   `json:"agent_id"`
		Action  string   `json:"action"`
		Service string   `json:"service"`
		Project string   `json:"project"`
		Targets []string `json:"targets"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	var targets []string
	for _, t := range body.Targets {
		if t = strings.TrimSpace(t); t != "" {
			targets = append(targets, t)
		}
	}
	cmd, err := enqueueCommand(portalCommand{
		AgentID:   strings.TrimSpace(body.AgentID),
		Action:    strings.TrimSpace(body.Action),
		Service:   strings.TrimSpace(body.Service),
		Project:   strings.TrimSpace(body.Project),
		Targets:   targets,
		CreatedBy: u.Username,
	})
	if err != nil {
		audit(u.Username, "", "commands.enqueue", body.AgentID+"/"+body.Service+body.Project, "fail", err.Error(), r.RemoteAddr)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	audit(u.Username, "", "commands.enqueue", cmd.ID, "ok", body.Action+" "+body.Service+body.Project, r.RemoteAddr)
	writeOK(w, map[string]any{"command": cmd})
}

// --- 一次性注册令牌（enroll，免人工复制长效 token）---

func apiEnrollTokensIssue(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		AgentID string `json:"agent_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	plain, t, err := issueEnrollToken(strings.TrimSpace(body.AgentID), u.Username)
	if err != nil {
		audit(u.Username, "", "enroll_tokens.issue", body.AgentID, "fail", err.Error(), r.RemoteAddr)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	audit(u.Username, "", "enroll_tokens.issue", t.ID, "ok", "agent="+t.AgentID, r.RemoteAddr)
	// 明文只返回这一次（30 分钟有效、单次使用）
	writeOK(w, map[string]any{"enroll_token": plain, "id": t.ID, "agent_id": t.AgentID, "expires_at": t.ExpiresAt})
}

// POST /api/agent/enroll —— 被管机自助换正式 token。
// 认证：enroll 令牌本身（一次性）+ 经边缘时机器证书
// （kind=agent 且 label==agent_id）双因子；同宿主机内网直连仅 enroll 令牌
// （网络隔离，与 heartbeat 的双因子策略一致）。
func apiAgentEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		EnrollToken string `json:"enroll_token"`
		AgentID     string `json:"agent_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	agentID := strings.TrimSpace(body.AgentID)
	if strings.TrimSpace(body.EnrollToken) == "" || agentID == "" {
		writeError(w, http.StatusBadRequest, "enroll_token and agent_id required")
		return
	}
	id := certFromRequest(r)
	if id.Trusted {
		c := findActiveCert(id.Serial, id.Fingerprint)
		if c == nil || c.Kind != "agent" || c.Label != agentID {
			log.Printf("portal: enroll cert mismatch serial=%s", maskMiddle(id.Serial))
			writeError(w, http.StatusForbidden, "agent certificate mismatch")
			return
		}
	}
	bound, err := consumeEnrollToken(strings.TrimSpace(body.EnrollToken), agentID)
	if err != nil {
		audit(agentID, "", "agent.enroll", agentID, "fail", err.Error(), r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "invalid enroll token")
		return
	}
	plain, tok, err := issueAgentToken(bound, "enroll:"+bound)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	audit("enroll:"+bound, "", "agent.enroll", tok.ID, "ok", "agent="+bound, r.RemoteAddr)
	writeOK(w, map[string]any{"token": plain, "id": tok.ID, "agent_id": bound})
}

// --- 审计 ---

func apiAuditList(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	entries := readAuditTail(200)
	writeOK(w, map[string]any{"entries": entries})
}

// POST /api/portal/backup（admin）—— 在线备份数据库快照到 data/backups/。
// cron 调这个接口即可（持 human 证书），只留最新 14 份。
func apiBackup(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	u := actorOf(r)
	name, err := backupDB()
	if err != nil {
		audit(u.Username, "", "db.backup", "", "fail", err.Error(), r.RemoteAddr)
		writeInternalError(w, err)
		return
	}
	audit(u.Username, "", "db.backup", name, "ok", "", r.RemoteAddr)
	writeOK(w, map[string]any{"backup": name})
}
