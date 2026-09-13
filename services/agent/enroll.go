package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// enroll 用一次性注册令牌换取正式 agent token（见 docs/agent-protocol.md）。

func enrollAgent(portalURL, agentID, enrollToken string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"enroll_token": enrollToken,
		"agent_id":     agentID,
	})
	opCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(opCtx, http.MethodPost,
		strings.TrimRight(portalURL, "/")+"/api/agent/enroll", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		OK    bool   `json:"ok"`
		Token string `json:"token"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK || !out.OK || out.Token == "" {
		return "", fmt.Errorf("enroll rejected: %s", out.Error)
	}
	return out.Token, nil
}
