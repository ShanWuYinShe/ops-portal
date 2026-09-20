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
	AgentID         string              `json:"portal_agent_id"`
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
		strings.TrimRight(portalURL, "/")+"/api/portal-agent/heartbeat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	// 出网经 agentClient：配了 AGENT_CERT/AGENT_KEY 时出示机器证书（边缘 mTLS）；
	// 同机 http 内网直连不受影响。
	resp, err := agentClient().Do(httpReq)
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
	agentID := getEnv("PORTAL_AGENT_ID", "")
	portalURL := getEnv("PORTAL_URL", "http://portal-backend:8319")
	projects := splitCSV(getEnv("PORTAL_AGENT_COMPOSE_PROJECTS", ""))
	sitesSpec := getEnv("PORTAL_AGENT_SITES", "")
	deployDirs := parseDeployDirs(getEnv("PORTAL_AGENT_DEPLOY_DIRS", ""))
	interval := getEnv("HEARTBEAT_INTERVAL", "10s")
	dur, err := time.ParseDuration(interval)
	if err != nil || dur <= 0 {
		dur = 10 * time.Second
	}
	if agentID == "" {
		log.Fatal("PORTAL_AGENT_ID is required")
	}

	// 本机 /health 探活端点（容器 healthcheck 用；默认 127.0.0.1:18080，
	// PORTAL_AGENT_HEALTH_ADDR 置空禁用）。
	startHealthServer(getEnv("PORTAL_AGENT_HEALTH_ADDR", "127.0.0.1:18080"))

	ctx := context.Background()
	// token 解析：PORTAL_AGENT_TOKEN / 落盘 / enroll 自注册三选一（见 enroll.go）。
	token, err := resolveToken(ctx, portalURL, agentID)
	if err != nil {
		log.Fatalf("token: %v", err)
	}

	cli, err := newDockerClient()
	if err != nil {
		log.Fatalf("docker client: %v", err)
	}
	defer cli.Close()

	hostname, _ := os.Hostname()
	log.Printf("portal-agent %s -> %s every %s (projects=%v)", agentID, portalURL, dur, projects)

	// 命令执行与心跳分离：deploy 最长 10 分钟，若在心跳循环里串行执行，
	// 期间心跳停发会被 portal 判为掉线并误发告警。单独 goroutine 串行执行
	// （channel 天然排队，不并发操作 docker），心跳不受执行时长影响。
	cmdQueue := make(chan []inboundCommand, 16)
	go func() {
		for cmds := range cmdQueue {
			drainCommands(ctx, cli, portalURL, token, agentID, cmds, projects, deployDirs)
		}
	}()

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
		recordHeartbeat(err)
		if err != nil {
			log.Printf("heartbeat: post failed: %v", err)
		} else if len(cmds) > 0 {
			cmdQueue <- cmds
		}
		<-tick.C
	}
}
