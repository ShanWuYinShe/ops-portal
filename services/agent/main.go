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

// agent 主循环：采集本机快照 → POST 心跳到 portal → 取回命令队列 →
// 本地执行 → 回传结果。

type heartbeatRequest struct {
	AgentID         string              `json:"agent_id"`
	Hostname        string              `json:"hostname"`
	ComposeProjects []string            `json:"compose_projects"`
	Sites           []siteSnapshot      `json:"sites"`
	Containers      []containerSnapshot `json:"containers"`
}

type heartbeatResponse struct {
	OK       bool             `json:"ok"`
	Commands []inboundCommand `json:"commands"`
}

var errHeartbeatRejected = errors.New("heartbeat rejected")

func postHeartbeat(ctx context.Context, portalURL, token string, req heartbeatRequest) ([]inboundCommand, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(opCtx, http.MethodPost,
		strings.TrimRight(portalURL, "/")+"/api/agent/heartbeat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out heartbeatResponse
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, errHeartbeatRejected
	}
	return out.Commands, nil
}

func main() {
	agentID := getEnv("AGENT_ID", "")
	portalURL := getEnv("PORTAL_URL", "http://portal-backend:8319")
	token := getEnv("AGENT_TOKEN", "")
	// 自助注册：无正式 token 时用一次性注册令牌换取（免人工复制）
	if token == "" {
		if enroll := getEnv("AGENT_ENROLL_TOKEN", ""); enroll != "" {
			var err error
			token, err = enrollAgent(portalURL, agentID, enroll)
			if err != nil {
				log.Fatalf("enroll failed: %v", err)
			}
			log.Printf("agent: enrolled %s, got agent token", agentID)
		}
	}
	projects := splitCSV(getEnv("AGENT_COMPOSE_PROJECTS", ""))
	sitesSpec := getEnv("AGENT_SITES", "")
	deployDirs := parseDeployDirs(getEnv("AGENT_DEPLOY_DIRS", ""))
	interval := getEnv("HEARTBEAT_INTERVAL", "10s")
	dur, err := time.ParseDuration(interval)
	if err != nil || dur <= 0 {
		dur = 10 * time.Second
	}
	if agentID == "" {
		log.Fatal("AGENT_ID is required")
	}
	if token == "" {
		log.Fatal("AGENT_TOKEN is required")
	}

	cli, err := newDockerClient()
	if err != nil {
		log.Fatalf("docker client: %v", err)
	}
	defer cli.Close()

	hostname, _ := os.Hostname()
	log.Printf("ops-agent %s -> %s every %s (projects=%v)", agentID, portalURL, dur, projects)

	ctx := context.Background()
	tick := time.NewTicker(dur)
	defer tick.Stop()
	for {
		containers, err := listContainers(ctx, cli, projects)
		if err != nil {
			log.Printf("heartbeat: docker list failed: %v", err)
		}
		req := heartbeatRequest{
			AgentID:         agentID,
			Hostname:        hostname,
			ComposeProjects: projects,
			Sites:           collectSites(ctx, sitesSpec),
			Containers:      containers,
		}
		cmds, err := postHeartbeat(ctx, portalURL, token, req)
		if err != nil {
			log.Printf("heartbeat: post failed: %v", err)
		} else {
			drainCommands(ctx, cli, portalURL, token, agentID, cmds, projects, deployDirs)
		}
		<-tick.C
	}
}
