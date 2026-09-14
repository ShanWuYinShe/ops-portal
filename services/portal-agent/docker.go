package main

import (
	"context"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// 容器快照（只读采集，按 compose 项目白名单过滤）。

type containerSnapshot struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Service string `json:"service"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Image   string `json:"image"`
}

// listContainers 按白名单项目列出容器。projects 为空 = 全部上报（本地 dev），
// 生产必须配白名单，避免把整台机器的容器都暴露给 portal。
func listContainers(ctx context.Context, cli *client.Client, projects []string) ([]containerSnapshot, error) {
	allow := map[string]bool{}
	for _, p := range projects {
		allow[p] = true
	}
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := cli.ContainerList(opCtx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	var out []containerSnapshot
	for _, c := range list {
		project := c.Labels["com.docker.compose.project"]
		if len(allow) > 0 && !allow[project] {
			continue
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		id := c.ID
		if len(id) > 12 {
			id = id[:12]
		}
		out = append(out, containerSnapshot{
			ID:      id,
			Name:    name,
			Project: project,
			Service: c.Labels["com.docker.compose.service"],
			State:   c.State,
			Status:  c.Status,
			Image:   c.Image,
		})
	}
	return out, nil
}

func newDockerClient() (*client.Client, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}
