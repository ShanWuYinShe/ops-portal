package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// 站点 git 版本采集：name:/path 逗号分隔，目录不存在或非 git 仓库则跳过。

type siteSnapshot struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Head   string `json:"head"`
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
}

func gitOne(ctx context.Context, dir string, args ...string) (string, bool) {
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(opCtx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func collectSites(ctx context.Context, spec string) []siteSnapshot {
	var out []siteSnapshot
	for _, item := range splitCSV(spec) {
		name, path, ok := strings.Cut(item, ":")
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		if !ok || name == "" || path == "" {
			continue
		}
		head, ok := gitOne(ctx, path, "rev-parse", "HEAD")
		if !ok {
			continue
		}
		branch, _ := gitOne(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
		dirtyOut, _ := gitOne(ctx, path, "status", "--porcelain")
		out = append(out, siteSnapshot{
			Name:   name,
			Path:   path,
			Head:   head,
			Branch: branch,
			Dirty:  strings.TrimSpace(dirtyOut) != "",
		})
	}
	return out
}
