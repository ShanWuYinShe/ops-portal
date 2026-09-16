package main

import (
	"testing"
	"time"
)

// 悬挂 acked 命令：agent 取走后未回传（崩溃/重启）超时标 failed，不再永久积压；
// 新鲜 acked 与老数据（无 acked_at、created_at 未超 TTL+超时）不受影响。
func TestStaleAckedExpiry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	db, err := getDB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	mk := func(agentID string) *portalCommand {
		c, err := enqueueCommand(portalCommand{AgentID: agentID, Action: "restart", Service: "nginx"})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		return c
	}
	age := func(id string, ackedAt, createdAt string) {
		t.Helper()
		if _, err := db.Exec(`UPDATE commands SET status='acked', acked_at=?, created_at=? WHERE id=?`,
			ackedAt, createdAt, id); err != nil {
			t.Fatalf("age %s: %v", id, err)
		}
	}
	statusOf := func(id string) (string, string) {
		t.Helper()
		var status, result string
		if err := db.QueryRow(`SELECT status, result FROM commands WHERE id=?`, id).Scan(&status, &result); err != nil {
			t.Fatalf("scan %s: %v", id, err)
		}
		return status, result
	}
	stale := time.Now().Add(-commandAckTimeout - time.Minute).UTC().Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	oldQueued := time.Now().Add(-commandTTL() - commandAckTimeout - time.Minute).UTC().Format(time.RFC3339)

	hung := mk("a1") // 取走后 31 分钟无回传 → failed
	age(hung.ID, stale, fresh)
	running := mk("a1") // 刚取走，正常执行中 → 保持 acked
	age(running.ID, fresh, fresh)
	legacy := mk("a1") // 老数据无 acked_at，刚 ack（created_at 新）→ 保持 acked
	age(legacy.ID, "", fresh)
	legacyHung := mk("a1") // 老数据无 acked_at 且排队早已超 TTL+超时 → failed
	age(legacyHung.ID, "", oldQueued)

	got := takeCommands("a1")
	if len(got) != 0 {
		t.Fatalf("take should only reap, got %+v", got)
	}
	if s, _ := statusOf(hung.ID); s != "failed" {
		t.Fatalf("hung acked status = %s, want failed", s)
	}
	if s, res := statusOf(running.ID); s != "acked" || res != "" {
		t.Fatalf("fresh acked = %s/%q, want acked with empty result", s, res)
	}
	if s, _ := statusOf(legacy.ID); s != "acked" {
		t.Fatalf("legacy fresh acked = %s, want acked", s)
	}
	if s, _ := statusOf(legacyHung.ID); s != "failed" {
		t.Fatalf("legacy hung acked = %s, want failed", s)
	}

	// 超时标 failed 后不得再下发
	if got := takeCommands("a1"); len(got) != 0 {
		t.Fatalf("failed commands must not be redelivered: %+v", got)
	}
}
