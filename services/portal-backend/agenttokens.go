package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// agent token 库（SQLite portal_agent_tokens 表）：provision 时签发，一次显示，可轮换可吊销。
// token 只存 SHA256 摘要（防库泄露直接可用），签发时返回明文一次。

type agentToken struct {
	ID        string `json:"id"`
	AgentID   string `json:"portal_agent_id"`
	Hash      string `json:"hash"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	Revoked   bool   `json:"revoked"`
	Kind      string `json:"kind,omitempty"` // agent（正式）/ enroll（一次性注册，30 分钟）
	ExpiresAt string `json:"expires_at,omitempty"`
	Used      bool   `json:"used,omitempty"`
}

func scanToken(row interface {
	Scan(dest ...any) error
}) (*agentToken, error) {
	t := &agentToken{}
	var revoked, used int
	err := row.Scan(&t.ID, &t.AgentID, &t.Hash, &t.CreatedBy, &t.CreatedAt,
		&revoked, &t.Kind, &t.ExpiresAt, &used)
	if err != nil {
		return nil, err
	}
	t.Revoked, t.Used = revoked != 0, used != 0
	if t.Kind == "" {
		t.Kind = "portal-agent" // 旧库存量正式 token 无 kind 字段
	}
	return t, nil
}

const tokenColumns = `id,portal_agent_id,hash,created_by,created_at,revoked,kind,expires_at,used`

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见；退化成固定串会签出可预测的 token/id，
		// 宁可当次请求失败（HTTP handler 内 panic 由 net/http recover）。
		panic("randHex: crypto/rand read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func insertToken(t agentToken) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO portal_agent_tokens (`+tokenColumns+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		t.ID, t.AgentID, t.Hash, t.CreatedBy, t.CreatedAt,
		boolInt(t.Revoked), t.Kind, t.ExpiresAt, boolInt(t.Used))
	return err
}

// issueAgentToken 签发 token，返回明文（只此一次）与记录。
func issueAgentToken(agentID, createdBy string) (string, *agentToken, error) {
	if agentID == "" {
		return "", nil, errors.New("portal_agent_id required")
	}
	plain := "opa_" + randHex(24)
	t := agentToken{
		ID: "tok-" + randHex(6), AgentID: agentID, Hash: sha256Hex(plain),
		CreatedBy: createdBy, CreatedAt: time.Now().UTC().Format(time.RFC3339), Kind: "portal-agent",
	}
	if err := insertToken(t); err != nil {
		return "", nil, err
	}
	return plain, &t, nil
}

// issueEnrollToken 签发一次性注册令牌（enroll_ 前缀，绑定 portal_agent_id、
// 30 分钟有效、单次使用）。被管机凭它调 POST /api/portal-agent/enroll 换正式
// token，免人工复制长效 token。库里只存摘要，与正式 token 一致。
func issueEnrollToken(agentID, createdBy string) (string, *agentToken, error) {
	if agentID == "" {
		return "", nil, errors.New("portal_agent_id required")
	}
	plain := "enroll_" + randHex(24)
	t := agentToken{
		ID: "enr-" + randHex(6), AgentID: agentID, Hash: sha256Hex(plain),
		CreatedBy: createdBy, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Kind:      "enroll",
		ExpiresAt: time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
	}
	if err := insertToken(t); err != nil {
		return "", nil, err
	}
	return plain, &t, nil
}

func findTokenByHash(h string) (*agentToken, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	t, err := scanToken(db.QueryRow(`SELECT `+tokenColumns+` FROM portal_agent_tokens WHERE hash=? LIMIT 1`, h))
	if err == sql.ErrNoRows {
		return nil, errors.New("invalid token")
	}
	return t, err
}

// consumeEnrollToken 校验并核销一次性注册令牌，返回绑定的 portal_agent_id。
// 过期/已用/已吊销/串号一律失败（调用方对客户端统一报 invalid，不区分原因）。
// 核销用条件 UPDATE 保证并发下单次语义。
func consumeEnrollToken(plain, agentID string) (string, error) {
	db, err := getDB()
	if err != nil {
		return "", err
	}
	h := sha256Hex(plain)
	t, err := findTokenByHash(h)
	if err != nil {
		return "", errors.New("invalid enroll token")
	}
	if t.Kind != "enroll" || t.Revoked || t.Used {
		return "", errors.New("invalid enroll token")
	}
	if t.ExpiresAt != "" {
		if exp, err := time.Parse(time.RFC3339, t.ExpiresAt); err == nil && time.Now().After(exp) {
			return "", errors.New("enroll token expired")
		}
	}
	if t.AgentID != agentID {
		return "", errors.New("portal_agent_id mismatch")
	}
	res, err := db.Exec(`UPDATE portal_agent_tokens SET used=1 WHERE id=? AND used=0`, t.ID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errors.New("invalid enroll token") // 并发重放：已被核销
	}
	return t.AgentID, nil
}

// validAgentToken 校验 token 明文是否有效（未吊销；enroll 令牌不可作正式 token）。
// 查表即按 sha256Hex(plain) 精确匹配，命中即有效（constantTimeEqual 在这里
// 恒真无意义，防时序由「库里只存摘要」保证）。
func validAgentToken(plain string) (string, bool) {
	t, err := findTokenByHash(sha256Hex(plain))
	if err != nil || t.Revoked || t.Kind == "enroll" {
		return "", false
	}
	return t.AgentID, true
}

func revokeAgentToken(id string) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	res, err := db.Exec(`UPDATE portal_agent_tokens SET revoked=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return nil
}

func listAgentTokens() []agentToken {
	db, err := getDB()
	if err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT ` + tokenColumns + ` FROM portal_agent_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []agentToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil
		}
		out = append(out, *t)
	}
	return out
}
