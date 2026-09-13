package main

import (
	"errors"
	"os"
	"sort"
	"sync"
	"time"
)

// 命令队列：portal 入队 → agent 心跳取走 → agent 本地执行 → 下次心跳回传结果。
// 白名单动作：start / stop / restart（容器名=compose service 名）；
// deploy（按 compose 项目重建受影响服务，project+targets）。

type portalCommand struct {
	ID        string   `json:"id"`
	AgentID   string   `json:"agent_id"`
	Action    string   `json:"action"` // start / stop / restart / deploy
	Service   string   `json:"service"`
	Project   string   `json:"project"`
	Targets   []string `json:"targets"`
	Status    string   `json:"status"` // queued / acked / done / failed
	CreatedBy string   `json:"created_by"`
	CreatedAt string   `json:"created_at"`
	Result    string   `json:"result"`
}

type commandStore struct {
	Commands []portalCommand `json:"commands"`
}

var commandsMu sync.Mutex

func commandsPath() string {
	return dataDir() + "/commands.json"
}

func loadCommands() (*commandStore, error) {
	s := &commandStore{}
	if err := readJSON(commandsPath(), s); err != nil {
		if os.IsNotExist(err) {
			return &commandStore{}, nil
		}
		return nil, err
	}
	if s.Commands == nil {
		s.Commands = []portalCommand{}
	}
	return s, nil
}

func saveCommands(s *commandStore) error {
	return atomicWriteJSON(commandsPath(), s, 0600)
}

var validActions = map[string]bool{"start": true, "stop": true, "restart": true, "deploy": true}

func enqueueCommand(cmd portalCommand) (*portalCommand, error) {
	if !validActions[cmd.Action] {
		return nil, errors.New("invalid action")
	}
	if cmd.AgentID == "" {
		return nil, errors.New("agent_id required")
	}
	if cmd.Action == "deploy" {
		if cmd.Project == "" || len(cmd.Targets) == 0 {
			return nil, errors.New("deploy requires project and targets")
		}
	} else if cmd.Service == "" {
		return nil, errors.New("service required")
	}
	commandsMu.Lock()
	defer commandsMu.Unlock()
	s, err := loadCommands()
	if err != nil {
		return nil, err
	}
	cmd.ID = "cmd-" + time.Now().UTC().Format("20060102-150405") + "-" + randHex(4)
	cmd.Status = "queued"
	cmd.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	s.Commands = append(s.Commands, cmd)
	if err := saveCommands(s); err != nil {
		return nil, err
	}
	return &cmd, nil
}

// takeCommands 取走该 agent 的 queued 命令并标 acked（心跳时调用）。
func takeCommands(agentID string) []portalCommand {
	commandsMu.Lock()
	defer commandsMu.Unlock()
	s, err := loadCommands()
	if err != nil {
		return nil
	}
	var out []portalCommand
	changed := false
	for i := range s.Commands {
		if s.Commands[i].AgentID == agentID && s.Commands[i].Status == "queued" {
			s.Commands[i].Status = "acked"
			out = append(out, s.Commands[i])
			changed = true
		}
	}
	if changed {
		_ = saveCommands(s)
	}
	return out
}

// reportCommand agent 回传执行结果。
func reportCommand(agentID, id, status, result string) error {
	if status != "done" && status != "failed" {
		return errors.New("invalid status")
	}
	commandsMu.Lock()
	defer commandsMu.Unlock()
	s, err := loadCommands()
	if err != nil {
		return err
	}
	for i := range s.Commands {
		if s.Commands[i].ID == id && s.Commands[i].AgentID == agentID {
			s.Commands[i].Status = status
			s.Commands[i].Result = result
			return saveCommands(s)
		}
	}
	return errors.New("not found")
}

func listCommands(agentID string) []portalCommand {
	commandsMu.Lock()
	defer commandsMu.Unlock()
	s, err := loadCommands()
	if err != nil {
		return nil
	}
	var out []portalCommand
	for _, c := range s.Commands {
		if agentID == "" || c.AgentID == agentID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}
