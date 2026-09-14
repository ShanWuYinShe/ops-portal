package main

import (
	"encoding/json"
	"errors"
	"time"
)

// 命令队列（SQLite commands 表）：portal 入队 → agent 心跳取走 →
// agent 本地执行 → 下次心跳回传结果。
// 白名单动作：start / stop / restart（容器名=compose service 名）；
// deploy（按 compose 项目重建受影响服务，project+targets）。

type portalCommand struct {
	ID        string   `json:"id"`
	AgentID   string   `json:"portal_agent_id"`
	Action    string   `json:"action"` // start / stop / restart / deploy
	Service   string   `json:"service"`
	Project   string   `json:"project"`
	Targets   []string `json:"targets"`
	Status    string   `json:"status"` // queued / acked / done / failed / expired
	CreatedBy string   `json:"created_by"`
	CreatedAt string   `json:"created_at"`
	Result    string   `json:"result"`
}

const commandColumns = `id,portal_agent_id,action,service,project,targets,status,created_by,created_at,result`

func marshalTargets(t []string) (string, error) {
	if t == nil {
		return "[]", nil
	}
	b, err := json.Marshal(t)
	return string(b), err
}

func unmarshalTargets(s string) []string {
	var out []string
	if s == "" || json.Unmarshal([]byte(s), &out) != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func scanCommand(row interface {
	Scan(dest ...any) error
}) (*portalCommand, error) {
	c := &portalCommand{}
	var targets string
	err := row.Scan(&c.ID, &c.AgentID, &c.Action, &c.Service, &c.Project,
		&targets, &c.Status, &c.CreatedBy, &c.CreatedAt, &c.Result)
	if err != nil {
		return nil, err
	}
	c.Targets = unmarshalTargets(targets)
	return c, nil
}

var validActions = map[string]bool{"start": true, "stop": true, "restart": true, "deploy": true}

// 已终结（done/failed/expired）命令的保留条数上限，防表无限增长。
const maxTerminalCommands = 500

// commandTTL 排队命令有效期：agent 长时间离线后上线不再执行过期命令。
// 环境变量 COMMAND_TTL（默认 24h；非法值回退默认）。
func commandTTL() time.Duration {
	d, err := time.ParseDuration(getEnv("COMMAND_TTL", "24h"))
	if err != nil || d <= 0 {
		return 24 * time.Hour
	}
	return d
}

// commandExpiredAt 解析命令创建时间（容错：解析失败视为不过期）。
func commandExpiredAt(createdAt string, now time.Time) bool {
	ts, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return false
	}
	return now.Sub(ts) > commandTTL()
}

func enqueueCommand(cmd portalCommand) (*portalCommand, error) {
	if !validActions[cmd.Action] {
		return nil, errors.New("invalid action")
	}
	if cmd.AgentID == "" {
		return nil, errors.New("portal_agent_id required")
	}
	if cmd.Action == "deploy" {
		if cmd.Project == "" || len(cmd.Targets) == 0 {
			return nil, errors.New("deploy requires project and targets")
		}
	} else if cmd.Service == "" {
		return nil, errors.New("service required")
	}
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	cmd.ID = "cmd-" + time.Now().UTC().Format("20060102-150405") + "-" + randHex(4)
	cmd.Status = "queued"
	cmd.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	tg, err := marshalTargets(cmd.Targets)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`INSERT INTO commands (`+commandColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		cmd.ID, cmd.AgentID, cmd.Action, cmd.Service, cmd.Project, tg,
		cmd.Status, cmd.CreatedBy, cmd.CreatedAt, cmd.Result); err != nil {
		return nil, err
	}
	pruneCommands()
	return &cmd, nil
}

// pruneCommands 裁剪终结命令：queued/acked 全留，done/failed/expired 只留最新 500 条。
func pruneCommands() {
	db, err := getDB()
	if err != nil {
		return
	}
	_, _ = db.Exec(`DELETE FROM commands WHERE status NOT IN ('queued','acked') AND id NOT IN (
		SELECT id FROM commands WHERE status NOT IN ('queued','acked')
		ORDER BY created_at DESC LIMIT ?)`, maxTerminalCommands)
}

// takeCommands 取走该 agent 的 queued 命令并标 acked（心跳时调用）。
// 已过有效期的 queued 命令标 expired，不再下发。
func takeCommands(agentID string) []portalCommand {
	db, err := getDB()
	if err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT `+commandColumns+` FROM commands WHERE portal_agent_id=? AND status='queued'`, agentID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type pending struct {
		id, createdAt string
		cmd           portalCommand
	}
	var list []pending
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil
		}
		list = append(list, pending{id: c.ID, createdAt: c.CreatedAt, cmd: *c})
	}
	rows.Close()
	now := time.Now()
	var out []portalCommand
	for _, p := range list {
		if commandExpiredAt(p.createdAt, now) {
			_, _ = db.Exec(`UPDATE commands SET status='expired', result=? WHERE id=?`,
				"expired: queued beyond TTL, not executed", p.id)
			continue
		}
		if _, err := db.Exec(`UPDATE commands SET status='acked' WHERE id=? AND status='queued'`, p.id); err != nil {
			continue
		}
		p.cmd.Status = "acked"
		out = append(out, p.cmd)
	}
	return out
}

// reportCommand agent 回传执行结果。
func reportCommand(agentID, id, status, result string) error {
	if status != "done" && status != "failed" {
		return errors.New("invalid status")
	}
	db, err := getDB()
	if err != nil {
		return err
	}
	res, err := db.Exec(`UPDATE commands SET status=?, result=? WHERE id=? AND portal_agent_id=?`,
		status, result, id, agentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return nil
}

func listCommands(agentID string) []portalCommand {
	db, err := getDB()
	if err != nil {
		return nil
	}
	q := `SELECT ` + commandColumns + ` FROM commands `
	var args []any
	if agentID != "" {
		q += `WHERE portal_agent_id=? `
		args = append(args, agentID)
	}
	q += `ORDER BY created_at DESC LIMIT 100`
	rs, err := db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rs.Close()
	out := []portalCommand{}
	for rs.Next() {
		c, err := scanCommand(rs)
		if err != nil {
			return nil
		}
		out = append(out, *c)
	}
	return out
}
