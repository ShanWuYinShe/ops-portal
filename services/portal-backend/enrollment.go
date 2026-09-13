package main

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// 注册令牌（enroll）：一次性、30 分钟有效、绑定 agent_id。
// admin 签发（enroll_ 前缀明文一次显示）→ 被管机以它调用
// POST /api/agent/enroll 换取正式 agent token（opsa_ 前缀），
// 免去在 portal 页面复制 token 再手工写入被管机 env 的人工环节。

const enrollTTL = 30 * time.Minute

func issueEnrollToken(agentID, createdBy string) (string, *agentToken, error) {
	if strings.TrimSpace(agentID) == "" {
		return "", nil, errors.New("agent_id required")
	}
	plain := "enroll_" + randHex(16)
	tokensMu.Lock()
	defer tokensMu.Unlock()
	s, err := loadTokens()
	if err != nil {
		return "", nil, err
	}
	t := agentToken{
		ID:        "tok-" + randHex(6),
		AgentID:   agentID,
		Hash:      sha256Hex(plain),
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Kind:      "enroll",
		ExpiresAt: time.Now().Add(enrollTTL).UTC().Format(time.RFC3339),
	}
	s.Tokens = append(s.Tokens, t)
	if err := saveTokens(s); err != nil {
		return "", nil, err
	}
	return plain, &t, nil
}

// enrollAgent 校验注册令牌并给 agent_id 签发正式 token（单次使用）。
func enrollAgent(enrollPlain, agentID string) (string, error) {
	if strings.TrimSpace(agentID) == "" {
		return "", errors.New("agent_id required")
	}
	tokensMu.Lock()
	defer tokensMu.Unlock()
	s, err := loadTokens()
	if err != nil {
		return "", err
	}
	h := sha256Hex(enrollPlain)
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if t.Kind != "enroll" || !constantTimeEqual(t.Hash, h) {
			continue
		}
		if t.Used || t.Revoked {
			return "", errors.New("enroll token already used or revoked")
		}
		if exp, err := time.Parse(time.RFC3339, t.ExpiresAt); err != nil || time.Now().After(exp) {
			return "", errors.New("enroll token expired")
		}
		if t.AgentID != agentID {
			return "", errors.New("agent_id mismatch")
		}
		t.Used = true
		plain := "opsa_" + randHex(24)
		s.Tokens = append(s.Tokens, agentToken{
			ID:        "tok-" + randHex(6),
			AgentID:   agentID,
			Hash:      sha256Hex(plain),
			CreatedBy: "enroll:" + t.ID,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Kind:      "agent",
		})
		if err := saveTokens(s); err != nil {
			return "", err
		}
		return plain, nil
	}
	return "", errors.New("invalid enroll token")
}

// POST /api/portal/agent-tokens/enroll-token（admin）
func apiAgentEnrollTokenIssue(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	audit(u.Username, "", "agent_tokens.enroll_issue", t.ID, "ok", "agent="+t.AgentID, r.RemoteAddr)
	// 明文只返回这一次
	writeOK(w, map[string]any{"enroll_token": plain, "id": t.ID, "agent_id": t.AgentID,
		"expires_at": t.ExpiresAt})
}

// POST /api/agent/enroll（无 cert、无正式 token，凭一次性注册令牌换正式 token）
func apiAgentEnroll(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		EnrollToken string `json:"enroll_token"`
		AgentID     string `json:"agent_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	plain, err := enrollAgent(strings.TrimSpace(body.EnrollToken), strings.TrimSpace(body.AgentID))
	if err != nil {
		audit("enroll:"+body.AgentID, "", "agent.enroll", body.AgentID, "fail", err.Error(), r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	audit("enroll:"+body.AgentID, "", "agent.enroll", body.AgentID, "ok", "issued agent token", r.RemoteAddr)
	writeOK(w, map[string]any{"token": plain, "agent_id": body.AgentID})
}
