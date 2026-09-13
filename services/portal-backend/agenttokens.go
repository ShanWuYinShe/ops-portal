package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"sync"
	"time"
)

// agent token 库：provision 时签发，一次显示，可轮换可吊销。
// token 只存 SHA256 摘要（防库泄露直接可用），签发时返回明文一次。

type agentToken struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	Hash      string `json:"hash"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	Revoked   bool   `json:"revoked"`
}

type tokenStore struct {
	Tokens []agentToken `json:"tokens"`
}

var tokensMu sync.Mutex

func tokensPath() string {
	return dataDir() + "/agent_tokens.json"
}

func loadTokens() (*tokenStore, error) {
	s := &tokenStore{}
	if err := readJSON(tokensPath(), s); err != nil {
		if os.IsNotExist(err) {
			return &tokenStore{}, nil
		}
		return nil, err
	}
	if s.Tokens == nil {
		s.Tokens = []agentToken{}
	}
	return s, nil
}

func saveTokens(s *tokenStore) error {
	return atomicWriteJSON(tokensPath(), s, 0600)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "randerr"
	}
	return hex.EncodeToString(b)
}

// issueAgentToken 签发 token，返回明文（只此一次）与记录。
func issueAgentToken(agentID, createdBy string) (string, *agentToken, error) {
	if agentID == "" {
		return "", nil, errors.New("agent_id required")
	}
	plain := "opsa_" + randHex(24)
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
	}
	s.Tokens = append(s.Tokens, t)
	if err := saveTokens(s); err != nil {
		return "", nil, err
	}
	return plain, &t, nil
}

// validAgentToken 校验 token 明文是否有效（未吊销）。
func validAgentToken(plain string) (string, bool) {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	s, err := loadTokens()
	if err != nil {
		return "", false
	}
	h := sha256Hex(plain)
	for _, t := range s.Tokens {
		if !t.Revoked && constantTimeEqual(t.Hash, h) {
			return t.AgentID, true
		}
	}
	return "", false
}

func revokeAgentToken(id string) error {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	s, err := loadTokens()
	if err != nil {
		return err
	}
	for i := range s.Tokens {
		if s.Tokens[i].ID == id {
			s.Tokens[i].Revoked = true
			return saveTokens(s)
		}
	}
	return errors.New("not found")
}

func listAgentTokens() []agentToken {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	s, err := loadTokens()
	if err != nil {
		return nil
	}
	out := append([]agentToken{}, s.Tokens...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}
