package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// deploy 执行器：代人敲 compose 命令，只重建受影响服务。
// 命令形态：docker compose -p <project> --project-directory <dir>
//   -f <dir>/docker-compose.yml up -d --build <services...>
// 约束：project 必须在 AGENT_COMPOSE_PROJECTS 白名单；dir 来自 AGENT_DEPLOY_DIRS 映射；
// service 名只允许 [a-zA-Z0-9_-]+；超时 10 分钟；输出截断 8KB 回传。
//
// 路径语义（关键）：dir 必须是宿主机（Docker daemon 视角）绝对路径，
// 且 agent 容器须以同路径只读挂载该目录（源 == 目标）。
// 原因：compose CLI 在 agent 容器内跑，-f 与相对卷先按容器内路径解析，
// 再发给 daemon 执行；同路径挂载保证两边解析一致。
// （OrbStack 下 daemon 直接 open 未挂载过的宿主机路径会失败；Linux 服务器
// 同路径挂载同样成立。）

type deploySpec struct {
	Project  string   `json:"project"`
	Services []string `json:"services"`
}

func validServiceName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// parseDeployDirs 解析 AGENT_DEPLOY_DIRS：project:/宿主机绝对路径 逗号分隔。
func parseDeployDirs(spec string) map[string]string {
	out := map[string]string{}
	for _, item := range splitCSV(spec) {
		name, path, ok := strings.Cut(item, ":")
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		if !ok || name == "" || path == "" {
			continue
		}
		out[name] = path
	}
	return out
}

func runDeploy(ctx context.Context, deployDirs map[string]string, projects []string, dep deploySpec) reportItem {
	allow := map[string]bool{}
	for _, p := range projects {
		allow[p] = true
	}
	if !allow[dep.Project] {
		return reportItem{Status: "failed", Result: fmt.Sprintf("project %q not allowed", dep.Project)}
	}
	dir, ok := deployDirs[dep.Project]
	if !ok || dir == "" {
		return reportItem{Status: "failed", Result: fmt.Sprintf("no deploy dir for project %q", dep.Project)}
	}
	if len(dep.Services) == 0 || len(dep.Services) > 10 {
		return reportItem{Status: "failed", Result: "services count must be 1..10"}
	}
	for _, s := range dep.Services {
		if !validServiceName(s) {
			return reportItem{Status: "failed", Result: fmt.Sprintf("invalid service name %q", s)}
		}
	}
	if !strings.HasPrefix(dir, "/") {
		return reportItem{Status: "failed", Result: "deploy dir must be an absolute host path"}
	}
	args := []string{"compose", "-p", dep.Project, "--project-directory", dir, "-f", dir + "/docker-compose.yml", "up", "-d", "--build"}
	args = append(args, dep.Services...)
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(opCtx, "docker", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	log.Printf("agent: deploy %v", args)
	err := cmd.Run()
	out := buf.String()
	if len(out) > 8192 {
		out = out[:8192] + "\n...[truncated]"
	}
	if err != nil {
		return reportItem{Status: "failed", Result: "exit: " + err.Error() + "\n" + out}
	}
	return reportItem{Status: "done", Result: out}
}
