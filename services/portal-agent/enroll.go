package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// 启动 token 解析：AGENT_TOKEN 优先；为空时读 AGENT_TOKEN_FILE 落盘；
// 都没有且 AGENT_ENROLL_TOKEN 存在时自助兑换（一次性），成功后落盘（若配了路径）。
func resolveToken(ctx context.Context, portalURL, agentID string) (string, error) {
	if tok := strings.TrimSpace(getEnv("PORTAL_AGENT_TOKEN", "")); tok != "" {
		return tok, nil
	}
	tokenFile := strings.TrimSpace(getEnv("PORTAL_AGENT_TOKEN_FILE", ""))
	if tokenFile != "" {
		if data, err := os.ReadFile(tokenFile); err == nil {
			if tok := strings.TrimSpace(string(data)); tok != "" {
				return tok, nil
			}
		}
	}
	enroll := strings.TrimSpace(getEnv("PORTAL_AGENT_ENROLL_TOKEN", ""))
	if enroll == "" {
		return "", errors.New("PORTAL_AGENT_TOKEN is required (or AGENT_ENROLL_TOKEN for self-registration)")
	}
	tok, err := exchangeEnrollToken(ctx, portalURL, agentID, enroll)
	if err != nil {
		return "", err
	}
	if tokenFile != "" {
		if err := os.WriteFile(tokenFile, []byte(tok+"\n"), 0600); err != nil {
			log.Printf("portal-agent: save token file failed: %v", err)
		}
	} else {
		log.Printf("portal-agent: enrolled ok; set AGENT_TOKEN or AGENT_TOKEN_FILE to persist across restarts")
	}
	return tok, nil
}

// exchangeEnrollToken 用一次性注册令牌换正式 token（免人工复制长效 token）。
// 出网经 agentClient（带机器证书，走边缘 mTLS；同机 http 内网直连不受影响）。
func exchangeEnrollToken(ctx context.Context, portalURL, agentID, enroll string) (string, error) {
	body, err := json.Marshal(map[string]string{"enroll_token": enroll, "portal_agent_id": agentID})
	if err != nil {
		return "", err
	}
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(opCtx, http.MethodPost,
		strings.TrimRight(portalURL, "/")+"/api/portal-agent/enroll", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := agentClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		OK    bool   `json:"ok"`
		Token string `json:"token"`
		Err   string `json:"error"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	if !out.OK || out.Token == "" {
		if out.Err == "" {
			out.Err = "enroll rejected"
		}
		return "", errors.New(out.Err)
	}
	return out.Token, nil
}
