package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// 命令执行：心跳取回命令 → 本地 docker 执行（白名单动作 + 项目白名单）→
// 经 /api/portal-agent/report 回传结果。容器按 compose service 名定位，
// 与 workwork 的 apiServiceAction 模式一致。

type inboundCommand struct {
	ID      string   `json:"id"`
	AgentID string   `json:"portal_agent_id"`
	Action  string   `json:"action"` // start / stop / restart / deploy
	Service string   `json:"service"`
	Project string   `json:"project"` // deploy 用：compose 项目名
	Targets []string `json:"targets"` // deploy 用：受影响服务列表
}

type reportItem struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Result string `json:"result"`
}

func findServiceContainer(ctx context.Context, cli *client.Client, service, project string, projects []string) (string, error) {
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := cli.ContainerList(opCtx, container.ListOptions{All: true})
	if err != nil {
		return "", err
	}
	allow := map[string]bool{}
	for _, p := range projects {
		allow[p] = true
	}
	for _, c := range list {
		if c.Labels["com.docker.compose.service"] != service {
			continue
		}
		proj := c.Labels["com.docker.compose.project"]
		// project 非空时限定 compose 项目：多项目同名 service（如 nginx）不再误命中。
		// 空串保持兼容：老 portal 下发的命令无 project，按白名单全局找。
		if project != "" && proj != project {
			continue
		}
		if len(allow) > 0 && !allow[proj] {
			continue
		}
		return c.ID, nil
	}
	if project != "" {
		return "", fmt.Errorf("service %q not found in project %q", service, project)
	}
	return "", fmt.Errorf("service %q not found in projects %v", service, projects)
}

// isSelfTarget 判断命令目标是否为 agent 自身容器。
// compose 默认 hostname 即容器短 ID，目标完整 ID 以它开头即命中。
func isSelfTarget(ctx context.Context, cli *client.Client, service, project string, projects []string, hostname string) bool {
	if hostname == "" {
		return false
	}
	id, err := findServiceContainer(ctx, cli, service, project, projects)
	if err != nil {
		return false
	}
	if strings.HasPrefix(id, hostname) {
		return true
	}
	short := id
	if len(short) > 12 {
		short = short[:12]
	}
	return strings.HasPrefix(hostname, short)
}

func execCommand(ctx context.Context, cli *client.Client, cmd inboundCommand, projects []string, deployDirs map[string]string) reportItem {
	switch cmd.Action {
	case "start", "stop", "restart":
	case "deploy":
		rep := runDeploy(ctx, deployDirs, projects, deploySpec{Project: cmd.Project, Services: cmd.Targets})
		rep.ID = cmd.ID
		return rep
	default:
		return reportItem{ID: cmd.ID, Status: "failed", Result: "invalid action"}
	}
	id, err := findServiceContainer(ctx, cli, cmd.Service, cmd.Project, projects)
	if err != nil {
		return reportItem{ID: cmd.ID, Status: "failed", Result: err.Error()}
	}
	opCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	timeout := 10
	switch cmd.Action {
	case "start":
		err = cli.ContainerStart(opCtx, id, container.StartOptions{})
	case "stop":
		err = cli.ContainerStop(opCtx, id, container.StopOptions{Timeout: &timeout})
	case "restart":
		err = cli.ContainerRestart(opCtx, id, container.StopOptions{Timeout: &timeout})
	}
	if err != nil {
		return reportItem{ID: cmd.ID, Status: "failed", Result: err.Error()}
	}
	return reportItem{ID: cmd.ID, Status: "done", Result: cmd.Action + " ok"}
}

func postReport(ctx context.Context, portalURL, token, agentID string, reps []reportItem) error {
	body, err := json.Marshal(map[string]any{"portal_agent_id": agentID, "reports": reps})
	if err != nil {
		return err
	}
	// deploy/restart 会重建 backend，回传时对方可能正在重启：退避重试。
	backoffs := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}
	var lastErr error
	for i := 0; i <= len(backoffs); i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffs[i-1]):
			}
		}
		if err := postReportOnce(ctx, portalURL, token, body); err != nil {
			lastErr = err
			log.Printf("portal-agent: report attempt %d failed: %v", i+1, err)
			continue
		}
		return nil
	}
	return lastErr
}

func postReportOnce(ctx context.Context, portalURL, token string, body []byte) error {
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(opCtx, http.MethodPost,
		strings.TrimRight(portalURL, "/")+"/api/portal-agent/report", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := agentClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("report status %d", resp.StatusCode)
	}
	return nil
}

func drainCommands(ctx context.Context, cli *client.Client, portalURL, token, agentID string, cmds []inboundCommand, projects []string, deployDirs map[string]string) {
	if len(cmds) == 0 {
		return
	}
	log.Printf("portal-agent: executing %d command(s)", len(cmds))
	hostname, _ := os.Hostname()
	for _, c := range cmds {
		log.Printf("portal-agent: %s %s (%s)", c.Action, c.Service, c.ID)
		// 目标容器就是 agent 自己时（hostname 即容器短 ID），restart/stop
		// 会杀掉本进程：先回传"即将执行"，结果由下次心跳的容器状态体现
		// （Up 时间重置即成功）。deploy 不杀自己，无需预回传。
		if (c.Action == "restart" || c.Action == "stop") && isSelfTarget(ctx, cli, c.Service, c.Project, projects, hostname) {
			rep := reportItem{ID: c.ID, Status: "done", Result: c.Action + " self: reported before exec, verify by container uptime"}
			if err := postReport(ctx, portalURL, token, agentID, []reportItem{rep}); err != nil {
				log.Printf("portal-agent: report %s failed: %v", c.ID, err)
			}
		}
		rep := execCommand(ctx, cli, c, projects, deployDirs)
		// 逐条立即回传：批量回传在自操作场景下永远发不出去。
		if err := postReport(ctx, portalURL, token, agentID, []reportItem{rep}); err != nil {
			log.Printf("portal-agent: report %s failed: %v", c.ID, err)
		}
	}
}
